// Mock VirtFusion API and scenarios. Drives the virtfusion module through the
// server lifecycle, including the byExtRelation user resolution and the
// server_id round-trip that HostCore performs between deploy and later calls.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type vfServer struct {
	Package   int
	Suspended bool
}

type vfPanel struct {
	usersByExt map[string]int // extRelationId -> user id
	nextUser   int
	servers    map[string]*vfServer // server id -> server
	nextServer int
	lastCreate map[string]any
}

func newVF() *vfPanel {
	return &vfPanel{
		usersByExt: map[string]int{},
		nextUser:   500,
		servers:    map[string]*vfServer{},
		nextServer: 400,
	}
}

func (p *vfPanel) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	ep := u.Path[strings.Index(u.Path, "/api/v1/")+len("/api/v1/"):]
	seg := strings.Split(ep, "/")

	switch {
	case method == "GET" && ep == "packages":
		return 200, `{"data":[{"id":1,"name":"Starter"}]}`

	case method == "GET" && len(seg) == 3 && seg[0] == "users" && seg[2] == "byExtRelation":
		if id, ok := p.usersByExt[seg[1]]; ok {
			return 200, fmt.Sprintf(`{"data":{"id":%d}}`, id)
		}
		return 404, `{"msg":"user not found"}`

	case method == "POST" && len(seg) == 4 && seg[0] == "users" && seg[3] == "resetPassword":
		if _, ok := p.usersByExt[seg[1]]; ok {
			return 201, `{"data":{"password":"gen-pw-9182"}}`
		}
		return 404, `{"msg":"user not found"}`

	case method == "POST" && ep == "users":
		uid := p.nextUser
		p.nextUser++
		p.usersByExt[str(body["extRelationId"])] = uid
		return 201, fmt.Sprintf(`{"data":{"id":%d}}`, uid)

	case method == "POST" && ep == "servers":
		p.lastCreate = body
		sid := fmt.Sprintf("%d", p.nextServer)
		p.nextServer++
		p.servers[sid] = &vfServer{Package: toInt(body["packageId"])}
		return 201, fmt.Sprintf(`{"data":{"id":%s}}`, sid)

	case method == "POST" && len(seg) == 3 && seg[0] == "servers" && seg[2] == "suspend":
		if s := p.servers[seg[1]]; s != nil {
			s.Suspended = true
			return 204, ``
		}
		return 404, `{"msg":"server not found"}`

	case method == "POST" && len(seg) == 3 && seg[0] == "servers" && seg[2] == "unsuspend":
		if s := p.servers[seg[1]]; s != nil {
			s.Suspended = false
			return 204, ``
		}
		return 404, `{"msg":"server not found"}`

	case method == "PUT" && len(seg) == 4 && seg[0] == "servers" && seg[2] == "package":
		if s := p.servers[seg[1]]; s != nil {
			s.Package = atoiSafe(seg[3])
			return 200, `{"data":{"id":1}}`
		}
		return 404, `{"msg":"server not found"}`

	case method == "DELETE" && len(seg) == 2 && seg[0] == "servers":
		if p.servers[seg[1]] != nil {
			delete(p.servers, seg[1])
			return 204, ``
		}
		return 404, `{"msg":"server not found"}`
	}
	return 404, `{}`
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runVirtfusion(wasm []byte) {
	vfLifecycle(wasm)
	vfConfigOptions(wasm)
}

func vfArgs(serverID, extra string) string {
	out := strings.Replace(baseArgs(extra), `"domain":"panel.test"`, `"domain":"vf.test"`, 1)
	if serverID != "" {
		out = strings.Replace(out, `"service_id":"1024"`, `"service_id":"1024","server_id":"`+serverID+`"`, 1)
	}
	return out
}

func vfLifecycle(wasm []byte) {
	fmt.Println("── connection / create (user+server) / suspend / unsuspend / change package / reset password / terminate")
	p := newVF()
	plugin := loadPlugin(wasm, p.route)

	assert("test connection", call(plugin, "TestModuleConnection", vfArgs("", ""))["success"] == true, nil)

	deployArgs := vfArgs("", strings.Join([]string{
		cfg("hypervisor_id", "2"), cfg("package_id", "7"), cfg("ipv4", "2"), cfg("memory", "4096"),
	}, ","))
	res := call(plugin, "DeployService", deployArgs)
	assert("deploy succeeds", res["success"] == true, res)
	assert("user created via byExtRelation (ext id 77)", p.usersByExt["77"] != 0, p.usersByExt)
	assert("server create sent packageId", toInt(p.lastCreate["packageId"]) == 7, p.lastCreate["packageId"])
	assert("server create sent hypervisorId", toInt(p.lastCreate["hypervisorId"]) == 2, p.lastCreate["hypervisorId"])
	assert("server create sent ipv4 count", toInt(p.lastCreate["ipv4"]) == 2, p.lastCreate["ipv4"])
	assert("server create linked user id", toInt(p.lastCreate["userId"]) == p.usersByExt["77"], p.lastCreate["userId"])
	assert("memory override forwarded", toInt(p.lastCreate["memory"]) == 4096, p.lastCreate["memory"])

	data, _ := res["data"].(map[string]any)
	sid := fmt.Sprintf("%d", toInt(data["server_id"]))
	assert("deploy returns server_id", p.servers[sid] != nil, data["server_id"])

	// Lifecycle calls carry the server_id back, as HostCore would.
	lc := vfArgs(sid, cfg("package_id", "9"))
	assert("suspend", call(plugin, "SuspendService", lc)["success"] == true, nil)
	assert("server suspended", p.servers[sid].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", lc)["success"] == true, nil)
	assert("server unsuspended", p.servers[sid].Suspended == false, nil)

	assert("change package", call(plugin, "UpdateService", lc)["success"] == true, nil)
	assert("package changed to 9", p.servers[sid].Package == 9, p.servers[sid].Package)

	pw := call(plugin, "ChangePassword", lc)
	assert("reset password", pw["success"] == true, pw)
	pwData, _ := pw["data"].(map[string]any)
	assert("generated password returned", pwData["password"] == "gen-pw-9182", pwData)

	assert("terminate", call(plugin, "TerminateService", lc)["success"] == true, nil)
	assert("server removed", p.servers[sid] == nil, nil)
	assert("terminate is idempotent (404 → success)", call(plugin, "TerminateService", lc)["success"] == true, nil)

	assert("deploy without ids fails", call(plugin, "DeployService", vfArgs("", ""))["success"] == false, nil)
}

func vfConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newVF()
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
	assert("hypervisor_id & package_id required", required["hypervisor_id"] && required["package_id"], required)
}

func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}
