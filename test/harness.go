// Verification harness for the HostCore Pterodactyl module.
//
// It loads the compiled WASM module exactly as the Module Engine does
// (EnableWasi, http_* host functions with the same request/response wire
// format) but routes HTTP to an in-memory mock Pterodactyl panel. This lets us
// drive DeployService / Suspend / Update / Terminate end-to-end and assert on
// the intelligent multi-port allocation behaviour.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	extism "github.com/extism/go-sdk"
)

// ---------------------------------------------------------------------------
// Mock Pterodactyl panel
// ---------------------------------------------------------------------------

type alloc struct {
	ID       int64
	IP       string
	Port     int
	Assigned bool
}

type server struct {
	ID          int64
	UUID        string
	Identifier  string
	ExternalID  string
	Node        int64
	Allocation  int64
	AllocIDs    []int64
	Suspended   bool
	Environment map[string]string
}

type panel struct {
	users      map[string]int64 // external_id -> user id
	usersByID  map[int64]map[string]string
	nextUser   int64
	servers    map[string]*server // external_id -> server
	byID       map[int64]*server
	nextServer int64
	allocs     []*alloc
	log        []string
}

func newPanel() *panel {
	p := &panel{
		users:      map[string]int64{},
		usersByID:  map[int64]map[string]string{},
		nextUser:   100,
		servers:    map[string]*server{},
		byID:       map[int64]*server{},
		nextServer: 5000,
	}
	// Two IPs. Only 1.2.3.4 can satisfy BOTH a SERVER_PORT in 25565-25570 and
	// a QUERY_PORT in 27015-27020. 1.2.3.5 has a matching server port but no
	// query port, so a naive allocator that ignores co-location would fail.
	id := int64(1)
	add := func(ip string, port int, assigned bool) {
		p.allocs = append(p.allocs, &alloc{ID: id, IP: ip, Port: port, Assigned: assigned})
		id++
	}
	add("1.2.3.4", 25565, false)
	add("1.2.3.4", 25566, false)
	add("1.2.3.4", 27016, false)
	add("1.2.3.4", 27017, false)
	add("1.2.3.5", 25567, false)
	add("1.2.3.5", 25568, false)
	// A stray assigned allocation on a third IP, used by the dedicated-IP test.
	add("1.2.3.6", 25569, true)
	add("1.2.3.6", 27018, false)
	return p
}

func (p *panel) allocByID(id int64) *alloc {
	for _, a := range p.allocs {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// route dispatches a mock HTTP request and returns (status, jsonBody).
func (p *panel) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	path := u.Path
	q := u.Query()
	p.log = append(p.log, method+" "+path+qstr(q))

	// strip the /api/application/ prefix
	const prefix = "/api/application/"
	idx := strings.Index(path, prefix)
	if idx < 0 {
		return 404, `{}`
	}
	ep := path[idx+len(prefix):]

	switch {
	case method == "GET" && strings.HasPrefix(ep, "nodes/") && strings.Contains(ep, "/allocations"):
		return p.getNodeAllocations()
	case method == "GET" && ep == "nodes":
		return 200, `{"meta":{"pagination":{"total":2}},"data":[{"attributes":{"id":1}}]}`
	case method == "GET" && strings.HasPrefix(ep, "users/external/"):
		return p.getUserExternal(pathHead(strings.TrimPrefix(ep, "users/external/")))
	case method == "GET" && ep == "users":
		return p.getUsersByEmail(q.Get("filter[email]"))
	case method == "POST" && ep == "users":
		return p.createUser(body)
	case method == "PATCH" && strings.HasPrefix(ep, "users/"):
		return 200, p.userJSON(mustID(strings.TrimPrefix(ep, "users/")))
	case method == "GET" && strings.HasPrefix(ep, "nests/"):
		return p.getEgg()
	case method == "GET" && strings.HasPrefix(ep, "servers/external/"):
		return p.getServerExternal(pathHead(strings.TrimPrefix(ep, "servers/external/")))
	case method == "POST" && ep == "servers":
		return p.createServer(body)
	case method == "PATCH" && strings.Contains(ep, "/build"):
		return p.buildServer(serverIDFrom(ep), body)
	case method == "PATCH" && strings.Contains(ep, "/startup"):
		return p.startupServer(serverIDFrom(ep), body)
	case method == "POST" && strings.HasSuffix(ep, "/suspend"):
		p.setSuspended(serverIDFrom(ep), true)
		return 204, ``
	case method == "POST" && strings.HasSuffix(ep, "/unsuspend"):
		p.setSuspended(serverIDFrom(ep), false)
		return 204, ``
	case method == "DELETE" && strings.HasPrefix(ep, "servers/"):
		return p.deleteServer(serverIDFrom(ep))
	}
	return 404, `{}`
}

func (p *panel) getNodeAllocations() (int, string) {
	var items []string
	for _, a := range p.allocs {
		items = append(items, fmt.Sprintf(
			`{"attributes":{"id":%d,"ip":%q,"port":%d,"assigned":%t}}`,
			a.ID, a.IP, a.Port, a.Assigned))
	}
	return 200, fmt.Sprintf(`{"meta":{"pagination":{"total_pages":1}},"data":[%s]}`,
		strings.Join(items, ","))
}

func (p *panel) getUserExternal(id string) (int, string) {
	if uid, ok := p.users[id]; ok {
		return 200, p.userJSON(uid)
	}
	return 404, `{}`
}

func (p *panel) getUsersByEmail(email string) (int, string) {
	for uid, u := range p.usersByID {
		if strings.EqualFold(u["email"], email) {
			return 200, fmt.Sprintf(`{"meta":{"pagination":{"total":1}},"data":[%s]}`, p.userAttrs(uid))
		}
	}
	return 200, `{"meta":{"pagination":{"total":0}},"data":[]}`
}

func (p *panel) createUser(body map[string]any) (int, string) {
	uid := p.nextUser
	p.nextUser++
	ext, _ := body["external_id"].(string)
	p.usersByID[uid] = map[string]string{
		"username":   str(body["username"]),
		"email":      str(body["email"]),
		"first_name": str(body["first_name"]),
		"last_name":  str(body["last_name"]),
	}
	if ext != "" {
		p.users[ext] = uid
	}
	return 201, p.userJSON(uid)
}

func (p *panel) userAttrs(uid int64) string {
	u := p.usersByID[uid]
	return fmt.Sprintf(`{"attributes":{"id":%d,"username":%q,"email":%q,"first_name":%q,"last_name":%q}}`,
		uid, u["username"], u["email"], u["first_name"], u["last_name"])
}
func (p *panel) userJSON(uid int64) string { return p.userAttrs(uid) }

func (p *panel) getEgg() (int, string) {
	// Egg declares MAX_PLAYERS and QUERY_PORT variables. SERVER_PORT is NOT a
	// variable — it comes from the allocation.
	return 200, `{"attributes":{
		"docker_image":"ghcr.io/pterodactyl/games:source",
		"startup":"./run --port {{SERVER_PORT}}",
		"relationships":{"variables":{"data":[
			{"attributes":{"env_variable":"MAX_PLAYERS","default_value":"20"}},
			{"attributes":{"env_variable":"QUERY_PORT","default_value":"0"}}
		]}}
	}}`
}

func (p *panel) getServerExternal(ext string) (int, string) {
	if s, ok := p.servers[ext]; ok {
		return 200, p.serverJSON(s)
	}
	return 404, `{}`
}

func (p *panel) createServer(body map[string]any) (int, string) {
	deploy, _ := body["deploy"].(map[string]any)
	ranges, _ := deploy["port_range"].([]any)
	dedicated, _ := deploy["dedicated_ip"].(bool)

	// find lowest free port within any of the ranges, honouring dedicated_ip.
	skip := map[string]bool{}
	if dedicated {
		for _, a := range p.allocs {
			if a.Assigned {
				skip[a.IP] = true
			}
		}
	}
	var chosen *alloc
	for _, rng := range ranges {
		lo, hi := parseRange(str(rng))
		for _, a := range p.allocs {
			if a.Assigned || skip[a.IP] {
				continue
			}
			if a.Port >= lo && a.Port <= hi {
				if chosen == nil || a.Port < chosen.Port {
					chosen = a
				}
			}
		}
		if chosen != nil {
			break
		}
	}
	if chosen == nil {
		return 400, `{"errors":[{"detail":"no allocation"}]}`
	}
	chosen.Assigned = true

	sid := p.nextServer
	p.nextServer++
	ext := str(body["external_id"])
	s := &server{
		ID: sid, UUID: fmt.Sprintf("uuid-%d", sid), Identifier: fmt.Sprintf("id%d", sid),
		ExternalID: ext, Node: 1, Allocation: chosen.ID, AllocIDs: []int64{chosen.ID},
		Environment: map[string]string{},
	}
	if env, ok := body["environment"].(map[string]any); ok {
		for k, v := range env {
			s.Environment[k] = str(v)
		}
	}
	p.servers[ext] = s
	p.byID[sid] = s
	return 201, p.serverJSON(s)
}

func (p *panel) buildServer(sid int64, body map[string]any) (int, string) {
	s := p.byID[sid]
	if s == nil {
		return 404, `{}`
	}
	if v, ok := body["allocation"]; ok {
		s.Allocation = toInt64(v)
	}
	if adds, ok := body["add_allocations"].([]any); ok {
		for _, a := range adds {
			id := toInt64(a)
			if al := p.allocByID(id); al != nil {
				al.Assigned = true
			}
			if !contains(s.AllocIDs, id) {
				s.AllocIDs = append(s.AllocIDs, id)
			}
		}
	}
	if rems, ok := body["remove_allocations"].([]any); ok {
		for _, r := range rems {
			id := toInt64(r)
			if al := p.allocByID(id); al != nil {
				al.Assigned = false
			}
			s.AllocIDs = remove(s.AllocIDs, id)
		}
	}
	return 200, p.serverJSON(s)
}

func (p *panel) startupServer(sid int64, body map[string]any) (int, string) {
	s := p.byID[sid]
	if s == nil {
		return 404, `{}`
	}
	if env, ok := body["environment"].(map[string]any); ok {
		for k, v := range env {
			s.Environment[k] = str(v)
		}
	}
	return 200, p.serverJSON(s)
}

func (p *panel) setSuspended(sid int64, v bool) {
	if s := p.byID[sid]; s != nil {
		s.Suspended = v
	}
}

func (p *panel) deleteServer(sid int64) (int, string) {
	s := p.byID[sid]
	if s == nil {
		return 404, `{}`
	}
	delete(p.byID, sid)
	delete(p.servers, s.ExternalID)
	return 204, ``
}

func (p *panel) serverJSON(s *server) string {
	var allocItems []string
	for _, id := range s.AllocIDs {
		if a := p.allocByID(id); a != nil {
			allocItems = append(allocItems, fmt.Sprintf(
				`{"attributes":{"id":%d,"ip":%q,"port":%d,"assigned":%t,"alias":""}}`,
				a.ID, a.IP, a.Port, a.Assigned))
		}
	}
	envItems := []string{}
	for k, v := range s.Environment {
		envItems = append(envItems, fmt.Sprintf("%q:%q", k, v))
	}
	return fmt.Sprintf(`{"attributes":{
		"id":%d,"uuid":%q,"identifier":%q,"external_id":%q,"node":%d,"allocation":%d,
		"container":{"environment":{%s,"STARTUP":"./run"}},
		"relationships":{"allocations":{"data":[%s]}}
	}}`, s.ID, s.UUID, s.Identifier, s.ExternalID, s.Node, s.Allocation,
		strings.Join(envItems, ","), strings.Join(allocItems, ","))
}

// ---------------------------------------------------------------------------
// Host-function wiring (mirrors the engine's functions/http.go wire format)
// ---------------------------------------------------------------------------

func hostFns(p *panel) []extism.HostFunction {
	make := func(method string) extism.HostFunction {
		return extism.NewHostFunctionWithStack("http_"+method,
			func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
				in, _ := plugin.ReadBytes(stack[0])
				var req struct {
					URL     string          `json:"url"`
					Headers map[string]any  `json:"headers"`
					Body    json.RawMessage `json:"body"`
				}
				_ = json.Unmarshal(in, &req)
				var body map[string]any
				if len(req.Body) > 0 {
					_ = json.Unmarshal(req.Body, &body)
				}
				status, respBody := p.route(method, req.URL, body)
				out := buildResp(status, respBody)
				off, _ := plugin.WriteBytes([]byte(out))
				stack[0] = off
			},
			[]extism.ValueType{extism.ValueTypePTR},
			[]extism.ValueType{extism.ValueTypePTR})
	}
	return []extism.HostFunction{
		make("GET"), make("POST"), make("PUT"), make("PATCH"), make("DELETE"),
	}
}

func buildResp(status int, body string) string {
	ok := status >= 200 && status < 300
	if body == "" {
		body = `""`
	}
	if !json.Valid([]byte(body)) {
		b, _ := json.Marshal(body)
		body = string(b)
	}
	return fmt.Sprintf(`{"status":%d,"ok":%t,"body":%s}`, status, ok, body)
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

var failures int

func main() {
	path := os.Getenv("WASM")
	if path == "" {
		path = "../modules/pterodactyl.wasm"
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("read wasm:", err)
		fmt.Println("build it first: make -C .. build   (or set WASM=/path/to/module.wasm)")
		os.Exit(1)
	}

	scenarioMultiPort(wasm)
	scenarioLifecycle(wasm)
	scenarioDedicatedPort(wasm)
	scenarioConfigOptions(wasm)

	fmt.Println(strings.Repeat("=", 60))
	if failures > 0 {
		fmt.Printf("FAILED: %d assertion(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("ALL SCENARIOS PASSED")
}

func newPlugin(wasm []byte, p *panel) *extism.Plugin {
	ctx := context.Background()
	manifest := extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: wasm}}}
	plugin, err := extism.NewPlugin(ctx, manifest, extism.PluginConfig{EnableWasi: true}, hostFns(p))
	if err != nil {
		fmt.Println("plugin load:", err)
		os.Exit(1)
	}
	return plugin
}

var debug = os.Getenv("DBG") != ""

func call(plugin *extism.Plugin, fn, args string) map[string]any {
	exit, out, err := plugin.Call(fn, []byte(args))
	if debug {
		fmt.Printf("  [dbg] %s exit=%d err=%v out=%s\n", fn, exit, err, string(out))
	}
	if err != nil {
		fmt.Printf("  call %s error: %v\n", fn, err)
		failures++
		return nil
	}
	var res map[string]any
	_ = json.Unmarshal(out, &res)
	return res
}

func baseArgs(extra string) string {
	return `{
		"domain":"panel.test",
		"api_key":"ptla_test",
		"service_id":"1024",
		"password":"secret123",
		"user":{"id":"77","email":"Alice@Example.com","first_name":"Alice","last_name":"Ng"},
		"service_config":[` + extra + `]
	}`
}

func cfg(k, v string) string { return fmt.Sprintf(`{"key":%q,"value":%q}`, k, v) }

func scenarioMultiPort(wasm []byte) {
	fmt.Println("── Scenario: multi-port deploy (SERVER_PORT + QUERY_PORT co-located)")
	p := newPanel()
	plugin := newPlugin(wasm, p)

	args := baseArgs(strings.Join([]string{
		cfg("nest_id", "1"), cfg("egg_id", "1"), cfg("location_id", "1"),
		cfg("cpu", "100"), cfg("memory", "2"), cfg("memory_unit", "GB"),
		cfg("disk", "10"), cfg("disk_unit", "GB"), cfg("swap", "0"),
		cfg("databases", "1"), cfg("backups", "2"), cfg("allocations", "0"),
		cfg("ports_ranges", `{"SERVER_PORT":"25565-25570","QUERY_PORT":"27015-27020"}`),
		cfg("default_variables", `{"MAX_PLAYERS":"30"}`),
	}, ","))

	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)
	data, _ := res["data"].(map[string]any)
	ports, _ := data["ports"].(map[string]any)
	sp := toInt(ports["SERVER_PORT"])
	qp := toInt(ports["QUERY_PORT"])
	assert("SERVER_PORT in range 25565-25570", sp >= 25565 && sp <= 25570, sp)
	assert("QUERY_PORT in range 27015-27020", qp >= 27015 && qp <= 27020, qp)

	// Both allocated ports must be on the same IP.
	ipOf := func(port int) string {
		for _, a := range p.allocs {
			if a.Port == port {
				return a.IP
			}
		}
		return "?"
	}
	assert("SERVER_PORT and QUERY_PORT co-located on one IP", ipOf(sp) == ipOf(qp) && ipOf(sp) == "1.2.3.4", ipOf(sp)+"/"+ipOf(qp))
	assert("address reported as ip:port", data["address"] == ipOf(sp)+":"+strconv.Itoa(sp), data["address"])

	// The server's environment must carry QUERY_PORT and NOT SERVER_PORT.
	s := p.servers["1024"]
	assert("QUERY_PORT injected into environment", s != nil && s.Environment["QUERY_PORT"] == strconv.Itoa(qp), envDump(s))
	assert("MAX_PLAYERS default override applied", s != nil && s.Environment["MAX_PLAYERS"] == "30", envDump(s))
	assert("SERVER_PORT NOT set as env var", s != nil && s.Environment["SERVER_PORT"] == "", envDump(s))
	assert("server has 2 allocations attached", s != nil && len(s.AllocIDs) == 2, s.AllocIDs)
}

func scenarioLifecycle(wasm []byte) {
	fmt.Println("── Scenario: suspend / unsuspend / update / terminate")
	p := newPanel()
	plugin := newPlugin(wasm, p)
	args := baseArgs(strings.Join([]string{
		cfg("nest_id", "1"), cfg("egg_id", "1"), cfg("location_id", "1"),
		cfg("cpu", "100"), cfg("memory", "1024"), cfg("disk", "5120"),
		cfg("ports_ranges", `{"SERVER_PORT":"25565-25570"}`),
	}, ","))

	assert("deploy", call(plugin, "DeployService", args)["success"] == true, nil)

	assert("suspend", call(plugin, "SuspendService", args)["success"] == true, nil)
	assert("server marked suspended", p.servers["1024"].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", args)["success"] == true, nil)
	assert("server marked unsuspended", p.servers["1024"].Suspended == false, nil)

	upd := baseArgs(strings.Join([]string{
		cfg("nest_id", "1"), cfg("egg_id", "1"), cfg("location_id", "1"),
		cfg("cpu", "200"), cfg("memory", "4"), cfg("memory_unit", "GB"),
		cfg("disk", "20"), cfg("disk_unit", "GB"),
		cfg("ports_ranges", `{"SERVER_PORT":"25565-25570"}`),
		cfg("default_variables", `{"MAX_PLAYERS":"64"}`),
	}, ","))
	updRes := call(plugin, "UpdateService", upd)
	assert("update succeeds", updRes["success"] == true, updRes)
	assert("update re-applied MAX_PLAYERS", p.servers["1024"].Environment["MAX_PLAYERS"] == "64", envDump(p.servers["1024"]))

	assert("terminate", call(plugin, "TerminateService", args)["success"] == true, nil)
	assert("server removed", p.servers["1024"] == nil, nil)

	// Deploy guard: terminating then re-deploying should work; suspending a
	// missing server should fail cleanly.
	sus := call(plugin, "SuspendService", args)
	assert("suspend missing server fails gracefully", sus["success"] == false, sus)
}

func scenarioDedicatedPort(wasm []byte) {
	fmt.Println("── Scenario: dedicated port pins the first port of the range")
	p := newPanel()
	plugin := newPlugin(wasm, p)
	args := baseArgs(strings.Join([]string{
		cfg("nest_id", "1"), cfg("egg_id", "1"), cfg("location_id", "1"),
		cfg("cpu", "100"), cfg("memory", "1024"), cfg("disk", "5120"),
		cfg("ports_ranges", `{"SERVER_PORT":"25565-25570"}`),
		cfg("dedicated_port", "on"),
	}, ","))
	res := call(plugin, "DeployService", args)
	assert("dedicated-port deploy succeeds", res["success"] == true, res)
	data, _ := res["data"].(map[string]any)
	ports, _ := data["ports"].(map[string]any)
	assert("SERVER_PORT pinned to 25565", toInt(ports["SERVER_PORT"]) == 25565, ports)
}

func scenarioConfigOptions(wasm []byte) {
	fmt.Println("── Scenario: GetServiceConfigOptions returns valid schema")
	p := newPanel()
	plugin := newPlugin(wasm, p)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	assert("schema has fields", len(data) >= 15, len(data))
	// Ensure ports_ranges is advertised and required.
	found := false
	for _, f := range data {
		m, _ := f.(map[string]any)
		if m["key"] == "ports_ranges" {
			found = m["required"] == true
		}
	}
	assert("ports_ranges advertised & required", found, nil)
}

// ---------------------------------------------------------------------------
// assertion + helpers
// ---------------------------------------------------------------------------

func assert(name string, ok bool, detail any) {
	if ok {
		fmt.Printf("  ✓ %s\n", name)
		return
	}
	failures++
	fmt.Printf("  ✗ %s  (got: %v)\n", name, detail)
}

func envDump(s *server) any {
	if s == nil {
		return "<nil server>"
	}
	return s.Environment
}

func qstr(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
func pathHead(s string) string {
	if i := strings.IndexAny(s, "?/"); i >= 0 {
		return s[:i]
	}
	return s
}
func serverIDFrom(ep string) int64 {
	// servers/<id>/... or servers/<id>
	rest := strings.TrimPrefix(ep, "servers/")
	return mustID(pathHead(rest))
}
func mustID(s string) int64 { n, _ := strconv.ParseInt(pathHead(s), 10, 64); return n }
func parseRange(s string) (int, int) {
	parts := strings.SplitN(s, "-", 2)
	lo, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi := lo
	if len(parts) == 2 {
		hi, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	return lo, hi
}
func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
func toInt(v any) int     { f, _ := v.(float64); return int(f) }
func toInt64(v any) int64 { f, _ := v.(float64); return int64(f) }
func contains(s []int64, v int64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
func remove(s []int64, v int64) []int64 {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
