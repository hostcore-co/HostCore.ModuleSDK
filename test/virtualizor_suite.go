// Mock Virtualizor Admin API and scenarios. The Admin API is a single endpoint
// selected by the "act" query parameter; the mock switches on it and asserts on
// the parameters the module sends and the vpsid round-trip.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type vzVPS struct {
	Running   bool
	Plan      string
	Suspended bool
}

type vzPanel struct {
	vpses    map[string]*vzVPS
	nextVPS  int
	lastAct  string
	lastQ    url.Values
	lastPass string
	powered  []string
}

func newVZ() *vzPanel {
	return &vzPanel{vpses: map[string]*vzVPS{}, nextVPS: 900}
}

func (p *vzPanel) route(method, rawURL string, _ map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	q := u.Query()
	p.lastAct = q.Get("act")
	p.lastQ = q

	if q.Get("apikey") == "" || q.Get("apipass") == "" {
		return 200, `{"error":{"auth":"missing credentials"}}`
	}

	switch p.lastAct {
	case "servers":
		return 200, `{"servers":{"1":{"server_name":"node1"}}}`
	case "addvs":
		id := fmt.Sprintf("%d", p.nextVPS)
		p.nextVPS++
		p.vpses[id] = &vzVPS{Plan: q.Get("plid")}
		return 200, fmt.Sprintf(`{"done":1,"vpsid":%q,"ips":["203.0.113.7"]}`, id)
	case "vs":
		switch {
		case q.Get("suspend") != "":
			if s := p.vpses[q.Get("suspend")]; s != nil {
				s.Suspended = true
				return 200, `{"done":1}`
			}
		case q.Get("unsuspend") != "":
			if s := p.vpses[q.Get("unsuspend")]; s != nil {
				s.Suspended = false
				return 200, `{"done":1}`
			}
		case q.Get("action") != "":
			if s := p.vpses[q.Get("vpsid")]; s != nil {
				p.powered = append(p.powered, q.Get("action"))
				s.Running = q.Get("action") != "stop"
				return 200, `{"done":1}`
			}
		case q.Get("delete") != "":
			if p.vpses[q.Get("delete")] != nil {
				delete(p.vpses, q.Get("delete"))
				return 200, `{"done":1}`
			}
		}
		return 200, `{"error":{"vs":"vps not found"}}`
	case "editvs":
		s := p.vpses[q.Get("vpsid")]
		if s == nil {
			return 200, `{"error":{"vs":"vps not found"}}`
		}
		if plid := q.Get("plid"); plid != "" {
			s.Plan = plid
		}
		if pw := q.Get("rootpass"); pw != "" {
			p.lastPass = pw
		}
		return 200, `{"done":1}`
	}
	return 200, `{"error":{"act":"unknown act"}}`
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runVirtualizor(wasm []byte) {
	vzLifecycle(wasm)
	vzRemoteBinding(wasm)
	vzConfigOptions(wasm)
	vzActions(wasm)
}

func vzArgs(serverID, extra string) string {
	out := strings.Replace(baseArgs(extra), `"domain":"panel.test"`, `"domain":"vz.test","api_pass":"secretpass"`, 1)
	if serverID != "" {
		out = strings.Replace(out, `"service_id":"1024"`, `"service_id":"1024","server_id":"`+serverID+`"`, 1)
	}
	return out
}

func vzLifecycle(wasm []byte) {
	fmt.Println("── connection / create / suspend / unsuspend / change plan / change password / terminate")
	p := newVZ()
	plugin := loadPlugin(wasm, p.route)

	assert("test connection", call(plugin, "TestModuleConnection", vzArgs("", ""))["success"] == true, nil)
	assert("credentials sent in query", p.lastQ.Get("apikey") == "key_test" && p.lastQ.Get("apipass") == "secretpass", p.lastQ)

	deployArgs := vzArgs("", strings.Join([]string{
		cfg("plan_id", "3"), cfg("os_id", "271"), cfg("ips", "2"), cfg("ram", "2048"),
	}, ","))
	res := call(plugin, "DeployService", deployArgs)
	assert("deploy succeeds", res["success"] == true, res)
	assert("addvs called", p.lastAct == "addvs", p.lastAct)
	assert("addvs sent plan", p.lastQ.Get("plid") == "3", p.lastQ.Get("plid"))
	assert("addvs sent os", p.lastQ.Get("osid") == "271", p.lastQ.Get("osid"))
	assert("addvs defaulted virt to kvm", p.lastQ.Get("virt") == "kvm", p.lastQ.Get("virt"))
	assert("addvs forwarded ram override", p.lastQ.Get("ram") == "2048", p.lastQ.Get("ram"))
	assert("addvs linked user email", p.lastQ.Get("user_email") == "Alice@Example.com", p.lastQ.Get("user_email"))

	data, _ := res["data"].(map[string]any)
	sid := str(data["server_id"])
	assert("deploy returns vpsid", p.vpses[sid] != nil, data["server_id"])
	assert("deploy reports assigned ip", data["ip"] == "203.0.113.7", data["ip"])

	lc := vzArgs(sid, cfg("plan_id", "8"))
	assert("suspend", call(plugin, "SuspendService", lc)["success"] == true, nil)
	assert("vps suspended", p.vpses[sid].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", lc)["success"] == true, nil)
	assert("vps unsuspended", p.vpses[sid].Suspended == false, nil)

	assert("change plan", call(plugin, "UpdateService", lc)["success"] == true, nil)
	assert("plan changed to 8", p.vpses[sid].Plan == "8", p.vpses[sid].Plan)

	pwArgs := strings.Replace(vzArgs(sid, ""), `"password":"secret123"`, `"password":"newRootPw"`, 1)
	assert("change password", call(plugin, "ChangePassword", pwArgs)["success"] == true, nil)
	assert("editvs received rootpass", p.lastPass == "newRootPw", p.lastPass)

	assert("terminate", call(plugin, "TerminateService", lc)["success"] == true, nil)
	assert("vps removed", p.vpses[sid] == nil, nil)
	assert("terminate missing vps fails", call(plugin, "TerminateService", lc)["success"] == false, nil)
}

func vzRemoteBinding(wasm []byte) {
	fmt.Println("── the VPS answers to the binding the platform replays")
	p := newVZ()
	plugin := loadPlugin(wasm, p.route)
	p.vpses["9001"] = &vzVPS{Plan: "3"}

	args := strings.Replace(vzArgs("", ""), `"service_id":"1024"`, `"service_id":"1024","remote_id":"9001"`, 1)

	assert("suspend by remote binding", call(plugin, "SuspendService", args)["success"] == true, nil)
	assert("vps suspended", p.vpses["9001"].Suspended == true, nil)
	assert("unsuspend by remote binding", call(plugin, "UnsuspendService", args)["success"] == true, nil)
	assert("vps unsuspended", p.vpses["9001"].Suspended == false, nil)
	assert("terminate by remote binding", call(plugin, "TerminateService", args)["success"] == true, nil)
	assert("vps removed", p.vpses["9001"] == nil, nil)
}

func vzConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newVZ()
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
	assert("plan_id & os_id required", required["plan_id"] && required["os_id"], required)
}

func vzActions(wasm []byte) {
	fmt.Println("── customer power actions")
	p := newVZ()
	plugin := loadPlugin(wasm, p.route)

	deploy := call(plugin, "DeployService", vzArgs("", cfg("plan_id", "3")+","+cfg("os_id", "270")))
	data, _ := deploy["data"].(map[string]any)
	sid := str(data["server_id"])

	withAction := func(a string) string {
		return strings.Replace(vzArgs(sid, ""), `"service_id":"1024"`, `"service_id":"1024","action":"`+a+`"`, 1)
	}

	for _, verb := range []string{"start", "restart", "stop"} {
		res := call(plugin, "ServiceAction", withAction(verb))
		assert(verb, res["success"] == true, res)
	}
	assert("every verb reached the Admin API", strings.Join(p.powered, ",") == "start,restart,stop", p.powered)
	assert("the VPS is left stopped", p.vpses[sid].Running == false, p.vpses[sid])

	bad := call(plugin, "ServiceAction", withAction("reinstall"))
	assert("an action Virtualizor has no endpoint for is refused", bad["success"] == false, bad)
	badData, _ := bad["data"].(map[string]any)
	assert("refusal carries the unsupported code", badData["code"] == "unsupported", bad["data"])

	missing := strings.Replace(withAction("start"), `"server_id":"`+sid+`"`, `"server_id":""`, 1)
	assert("an action without a VPS id is refused", call(plugin, "ServiceAction", missing)["success"] == false, nil)
}
