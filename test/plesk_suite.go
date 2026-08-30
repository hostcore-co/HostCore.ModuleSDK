// Mock Plesk REST API and scenarios. Drives the plesk module through the
// subscription lifecycle and asserts on the CLI utilities and arguments it
// sends through the REST CLI gateway.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type pleskSub struct {
	Owner     string
	Plan      string
	Suspended bool
}

type pleskPanel struct {
	customers map[string]bool
	subs      map[string]*pleskSub // domain -> subscription
	lastCLI   string               // "utility arg0 arg1 ..."
}

func newPlesk() *pleskPanel {
	return &pleskPanel{customers: map[string]bool{}, subs: map[string]*pleskSub{}}
}

// cliParams pulls the argument list out of the CLI-gateway request body.
func cliParams(body map[string]any) []string {
	raw, _ := body["params"].([]any)
	out := make([]string, 0)
	for _, v := range raw {
		out = append(out, str(v))
	}
	return out
}

// arg returns the value following flag in a CLI argument list.
func arg(params []string, flag string) string {
	for i := 0; i < len(params)-1; i++ {
		if params[i] == flag {
			return params[i+1]
		}
	}
	return ""
}

func (p *pleskPanel) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	ep := u.Path[strings.Index(u.Path, "/api/v2/")+len("/api/v2/"):]

	if method == "GET" && ep == "server" {
		return 200, `{"hostname":"plesk.test"}`
	}
	if method == "POST" && strings.HasPrefix(ep, "cli/") && strings.HasSuffix(ep, "/call") {
		utility := strings.TrimSuffix(strings.TrimPrefix(ep, "cli/"), "/call")
		params := cliParams(body)
		p.lastCLI = utility + " " + strings.Join(params, " ")
		return p.cli(utility, params)
	}
	return 404, `{}`
}

func (p *pleskPanel) cli(utility string, params []string) (int, string) {
	ok := func() (int, string) { return 200, `{"code":0,"stdout":"ok","stderr":""}` }
	fail := func(msg string) (int, string) {
		return 200, fmt.Sprintf(`{"code":1,"stdout":"","stderr":%q}`, msg)
	}
	if len(params) < 2 {
		return fail("missing arguments")
	}
	flag, target := params[0], params[1]

	switch utility {
	case "admin":
		if flag == "--get-login-link" {
			if p.customers[arg(params, "-user")] {
				return 200, `{"code":0,"stdout":"https://plesk.test:8443/login?secret=abc123\n","stderr":""}`
			}
			return fail("customer does not exist")
		}
	case "customer":
		switch flag {
		case "--info":
			if p.customers[target] {
				return ok()
			}
			return fail("customer does not exist")
		case "--create":
			p.customers[target] = true
			return ok()
		}
	case "subscription":
		switch flag {
		case "--create":
			if p.subs[target] != nil {
				return fail("subscription exists")
			}
			p.subs[target] = &pleskSub{Owner: arg(params, "-owner"), Plan: arg(params, "-service-plan")}
			return ok()
		case "--suspend":
			if s := p.subs[target]; s != nil {
				s.Suspended = true
				return ok()
			}
			return fail("no such subscription")
		case "--activate":
			if s := p.subs[target]; s != nil {
				s.Suspended = false
				return ok()
			}
			return fail("no such subscription")
		case "--remove":
			if p.subs[target] != nil {
				delete(p.subs, target)
				return ok()
			}
			return fail("no such subscription")
		case "--switch-subscription":
			if s := p.subs[target]; s != nil {
				s.Plan = arg(params, "-service-plan")
				return ok()
			}
			return fail("no such subscription")
		case "--update":
			if p.subs[target] != nil {
				return ok()
			}
			return fail("no such subscription")
		}
	}
	return fail("unknown command")
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runPlesk(wasm []byte) {
	pleskLifecycle(wasm)
	pleskConfigOptions(wasm)
	pleskActions(wasm)
}

func pleskArgs(extra string) string {
	return strings.Replace(baseArgs(extra), `"domain":"panel.test"`, `"domain":"plesk.test"`, 1)
}

func pleskLifecycle(wasm []byte) {
	fmt.Println("── connection / create (customer+subscription) / suspend / unsuspend / switch plan / change password / terminate")
	p := newPlesk()
	plugin := loadPlugin(wasm, p.route)

	assert("test connection", call(plugin, "TestModuleConnection", pleskArgs(""))["success"] == true, nil)

	args := pleskArgs(strings.Join([]string{
		cfg("domain", "acme.com"), cfg("plan", "Unlimited"), cfg("ip", "192.0.2.20"),
	}, ","))

	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)
	data, _ := res["data"].(map[string]any)
	cust := str(data["customer"])
	assert("customer created", p.customers[cust], p.customers)
	assert("subscription created for domain", p.subs["acme.com"] != nil, p.subs)
	assert("subscription owned by customer", p.subs["acme.com"].Owner == cust, p.subs["acme.com"])
	assert("subscription on requested plan", p.subs["acme.com"].Plan == "Unlimited", p.subs["acme.com"])
	assert("ip forwarded to create", strings.Contains(p.lastCLI, "-ip 192.0.2.20"), p.lastCLI)

	assert("suspend", call(plugin, "SuspendService", args)["success"] == true, nil)
	assert("subscription suspended", p.subs["acme.com"].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", args)["success"] == true, nil)
	assert("subscription active", p.subs["acme.com"].Suspended == false, nil)

	upd := pleskArgs(strings.Join([]string{cfg("domain", "acme.com"), cfg("plan", "Pro")}, ","))
	assert("switch plan", call(plugin, "UpdateService", upd)["success"] == true, nil)
	assert("plan switched", p.subs["acme.com"].Plan == "Pro", p.subs["acme.com"])

	assert("change password", call(plugin, "ChangePassword", args)["success"] == true, nil)
	assert("password change hit subscription --update", strings.HasPrefix(p.lastCLI, "subscription --update acme.com"), p.lastCLI)

	assert("terminate", call(plugin, "TerminateService", args)["success"] == true, nil)
	assert("subscription removed", p.subs["acme.com"] == nil, nil)
	assert("suspend missing subscription fails", call(plugin, "SuspendService", args)["success"] == false, nil)
}

func pleskConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newPlesk()
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
	assert("domain & plan required", required["domain"] && required["plan"], required)
}

func pleskActions(wasm []byte) {
	fmt.Println("── single sign-on into Plesk")
	p := newPlesk()
	plugin := loadPlugin(wasm, p.route)

	base := pleskArgs(cfg("domain", "acme.com") + "," + cfg("plan", "Unlimited"))
	deploy := call(plugin, "DeployService", base)
	assert("deploy succeeds", deploy["success"] == true, deploy)

	withAction := func(a string) string {
		return strings.Replace(base, `"service_id":"1024"`, `"service_id":"1024","action":"`+a+`"`, 1)
	}

	sso := call(plugin, "ServiceAction", withAction("sso"))
	assert("sso succeeds", sso["success"] == true, sso)
	assert("the admin utility minted the link", strings.HasPrefix(p.lastCLI, "admin --get-login-link"), p.lastCLI)
	data, _ := sso["data"].(map[string]any)
	assert("the login URL is returned, trimmed", data["url"] == "https://plesk.test:8443/login?secret=abc123", data["url"])

	bad := call(plugin, "ServiceAction", withAction("restart"))
	assert("Plesk has no power actions to offer", bad["success"] == false, bad)
	badData, _ := bad["data"].(map[string]any)
	assert("refusal carries the unsupported code", badData["code"] == "unsupported", bad["data"])
}
