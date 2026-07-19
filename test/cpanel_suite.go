// Mock WHM API and scenarios. Drives the cPanel module through the account
// lifecycle and asserts on the WHM function names and parameters it sends.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type whmAccount struct {
	Domain    string
	Plan      string
	Suspended bool
}

type whmPanel struct {
	accounts map[string]*whmAccount // username -> account
	lastFunc string
	lastQ    url.Values
}

func newWHM() *whmPanel {
	return &whmPanel{accounts: map[string]*whmAccount{}}
}

func (p *whmPanel) route(method, rawURL string, _ map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	q := u.Query()
	fn := pathHead(u.Path[strings.Index(u.Path, "/json-api/")+len("/json-api/"):])
	p.lastFunc = fn
	p.lastQ = q

	fail := func(reason string) (int, string) {
		return 200, fmt.Sprintf(`{"metadata":{"result":0,"reason":%q}}`, reason)
	}
	ok := func(data string) (int, string) {
		if data == "" {
			data = "{}"
		}
		return 200, fmt.Sprintf(`{"metadata":{"result":1,"reason":"OK"},"data":%s}`, data)
	}

	switch fn {
	case "version":
		return ok(`{"version":"110.0.5"}`)
	case "createacct":
		user := q.Get("username")
		if _, exists := p.accounts[user]; exists {
			return fail("account exists")
		}
		p.accounts[user] = &whmAccount{Domain: q.Get("domain"), Plan: q.Get("plan")}
		return ok(`{"ip":"192.0.2.10","nameserver":"ns1.example.com"}`)
	case "suspendacct":
		if a := p.accounts[q.Get("user")]; a != nil {
			a.Suspended = true
			return ok("")
		}
		return fail("no such account")
	case "unsuspendacct":
		if a := p.accounts[q.Get("user")]; a != nil {
			a.Suspended = false
			return ok("")
		}
		return fail("no such account")
	case "removeacct":
		if _, ok2 := p.accounts[q.Get("user")]; ok2 {
			delete(p.accounts, q.Get("user"))
			return ok("")
		}
		return fail("no such account")
	case "changepackage":
		if a := p.accounts[q.Get("user")]; a != nil {
			a.Plan = q.Get("pkg")
			return ok("")
		}
		return fail("no such account")
	case "passwd":
		if p.accounts[q.Get("user")] != nil {
			return ok(`{"result":[{"status":1}]}`)
		}
		return fail("no such account")
	}
	return fail("unknown function " + fn)
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runCpanel(wasm []byte) {
	cpanelLifecycle(wasm)
	cpanelConfigOptions(wasm)
}

func cpanelArgs(extra string) string {
	// domain here is the WHM host; the account domain is a service_config value.
	return strings.Replace(baseArgs(extra), `"domain":"panel.test"`, `"domain":"whm.test","username":"root"`, 1)
}

func cpanelLifecycle(wasm []byte) {
	fmt.Println("── connection / create / suspend / unsuspend / change package / change password / terminate")
	p := newWHM()
	plugin := loadPlugin(wasm, p.route)

	assert("test connection", call(plugin, "TestModuleConnection", cpanelArgs(""))["success"] == true, nil)

	args := cpanelArgs(strings.Join([]string{
		cfg("domain", "acme.com"), cfg("package", "starter"), cfg("dedicated_ip", "on"),
	}, ","))

	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)
	assert("createacct called", p.lastFunc == "createacct", p.lastFunc)
	assert("createacct sent plan", p.lastQ.Get("plan") == "starter", p.lastQ.Get("plan"))
	assert("createacct sent dedicated ip flag", p.lastQ.Get("ip") == "y", p.lastQ.Get("ip"))
	data, _ := res["data"].(map[string]any)
	uname := str(data["username"])
	assert("username derived from domain", strings.HasPrefix(uname, "acme"), uname)
	assert("account created on panel", p.accounts[uname] != nil, uname)
	assert("deploy reports account ip", data["ip"] == "192.0.2.10", data["ip"])

	assert("suspend", call(plugin, "SuspendService", args)["success"] == true, nil)
	assert("account suspended", p.accounts[uname].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", args)["success"] == true, nil)
	assert("account unsuspended", p.accounts[uname].Suspended == false, nil)

	upd := cpanelArgs(strings.Join([]string{cfg("domain", "acme.com"), cfg("package", "pro")}, ","))
	assert("update package", call(plugin, "UpdateService", upd)["success"] == true, nil)
	assert("changepackage applied", p.accounts[uname].Plan == "pro", p.accounts[uname].Plan)

	assert("change password", call(plugin, "ChangePassword", args)["success"] == true, nil)
	assert("passwd targeted the account", p.lastFunc == "passwd" && p.lastQ.Get("user") == uname, p.lastQ.Get("user"))

	assert("terminate", call(plugin, "TerminateService", args)["success"] == true, nil)
	assert("account removed", p.accounts[uname] == nil, nil)

	assert("suspend missing account fails", call(plugin, "SuspendService", args)["success"] == false, nil)
}

func cpanelConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newWHM()
	plugin := loadPlugin(wasm, p.route)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	required := map[string]bool{}
	for _, f := range data {
		m, _ := f.(map[string]any)
		if m["required"] == true {
			required[str(m["key"])] = true
		}
	}
	assert("domain & package required", required["domain"] && required["package"], required)
}
