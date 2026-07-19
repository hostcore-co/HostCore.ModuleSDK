// Mock Pterodactyl panel and scenarios. Drives the pterodactyl module through
// deploy / suspend / update / terminate and asserts on the intelligent
// multi-port allocation behaviour.
package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type pteroAlloc struct {
	ID       int64
	IP       string
	Port     int
	Assigned bool
}

type pteroServer struct {
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

type pteroPanel struct {
	users      map[string]int64
	usersByID  map[int64]map[string]string
	nextUser   int64
	servers    map[string]*pteroServer
	byID       map[int64]*pteroServer
	nextServer int64
	allocs     []*pteroAlloc
}

func newPtero() *pteroPanel {
	p := &pteroPanel{
		users:      map[string]int64{},
		usersByID:  map[int64]map[string]string{},
		nextUser:   100,
		servers:    map[string]*pteroServer{},
		byID:       map[int64]*pteroServer{},
		nextServer: 5000,
	}
	// Two IPs. Only 1.2.3.4 can satisfy BOTH a SERVER_PORT in 25565-25570 and a
	// QUERY_PORT in 27015-27020. 1.2.3.5 has a matching server port but no query
	// port, so a naive allocator that ignores co-location would fail.
	id := int64(1)
	add := func(ip string, port int, assigned bool) {
		p.allocs = append(p.allocs, &pteroAlloc{ID: id, IP: ip, Port: port, Assigned: assigned})
		id++
	}
	add("1.2.3.4", 25565, false)
	add("1.2.3.4", 25566, false)
	add("1.2.3.4", 27016, false)
	add("1.2.3.4", 27017, false)
	add("1.2.3.5", 25567, false)
	add("1.2.3.5", 25568, false)
	add("1.2.3.6", 25569, true)
	add("1.2.3.6", 27018, false)
	return p
}

func (p *pteroPanel) allocByID(id int64) *pteroAlloc {
	for _, a := range p.allocs {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (p *pteroPanel) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	path := u.Path
	q := u.Query()

	const prefix = "/api/application/"
	idx := strings.Index(path, prefix)
	if idx < 0 {
		return 404, `{}`
	}
	ep := path[idx+len(prefix):]

	switch {
	case method == "GET" && strings.HasPrefix(ep, "nodes/") && strings.Contains(ep, "/allocations"):
		return p.nodeAllocations()
	case method == "GET" && ep == "nodes":
		return 200, `{"meta":{"pagination":{"total":2}},"data":[{"attributes":{"id":1}}]}`
	case method == "GET" && strings.HasPrefix(ep, "users/external/"):
		return p.userExternal(pathHead(strings.TrimPrefix(ep, "users/external/")))
	case method == "GET" && ep == "users":
		return p.usersByEmail(q.Get("filter[email]"))
	case method == "POST" && ep == "users":
		return p.createUser(body)
	case method == "PATCH" && strings.HasPrefix(ep, "users/"):
		return 200, p.userJSON(mustID(strings.TrimPrefix(ep, "users/")))
	case method == "GET" && strings.HasPrefix(ep, "nests/"):
		return p.egg()
	case method == "GET" && strings.HasPrefix(ep, "servers/external/"):
		return p.serverExternal(pathHead(strings.TrimPrefix(ep, "servers/external/")))
	case method == "POST" && ep == "servers":
		return p.createServer(body)
	case method == "PATCH" && strings.Contains(ep, "/build"):
		return p.buildServer(pteroServerID(ep), body)
	case method == "PATCH" && strings.Contains(ep, "/startup"):
		return p.startupServer(pteroServerID(ep), body)
	case method == "POST" && strings.HasSuffix(ep, "/suspend"):
		p.setSuspended(pteroServerID(ep), true)
		return 204, ``
	case method == "POST" && strings.HasSuffix(ep, "/unsuspend"):
		p.setSuspended(pteroServerID(ep), false)
		return 204, ``
	case method == "DELETE" && strings.HasPrefix(ep, "servers/"):
		return p.deleteServer(pteroServerID(ep))
	}
	return 404, `{}`
}

func (p *pteroPanel) nodeAllocations() (int, string) {
	var items []string
	for _, a := range p.allocs {
		items = append(items, fmt.Sprintf(
			`{"attributes":{"id":%d,"ip":%q,"port":%d,"assigned":%t}}`,
			a.ID, a.IP, a.Port, a.Assigned))
	}
	return 200, fmt.Sprintf(`{"meta":{"pagination":{"total_pages":1}},"data":[%s]}`, strings.Join(items, ","))
}

func (p *pteroPanel) userExternal(id string) (int, string) {
	if uid, ok := p.users[id]; ok {
		return 200, p.userJSON(uid)
	}
	return 404, `{}`
}

func (p *pteroPanel) usersByEmail(email string) (int, string) {
	for uid, u := range p.usersByID {
		if strings.EqualFold(u["email"], email) {
			return 200, fmt.Sprintf(`{"meta":{"pagination":{"total":1}},"data":[%s]}`, p.userJSON(uid))
		}
	}
	return 200, `{"meta":{"pagination":{"total":0}},"data":[]}`
}

func (p *pteroPanel) createUser(body map[string]any) (int, string) {
	uid := p.nextUser
	p.nextUser++
	p.usersByID[uid] = map[string]string{
		"username":   str(body["username"]),
		"email":      str(body["email"]),
		"first_name": str(body["first_name"]),
		"last_name":  str(body["last_name"]),
	}
	if ext := str(body["external_id"]); ext != "" {
		p.users[ext] = uid
	}
	return 201, p.userJSON(uid)
}

func (p *pteroPanel) userJSON(uid int64) string {
	u := p.usersByID[uid]
	return fmt.Sprintf(`{"attributes":{"id":%d,"username":%q,"email":%q,"first_name":%q,"last_name":%q}}`,
		uid, u["username"], u["email"], u["first_name"], u["last_name"])
}

func (p *pteroPanel) egg() (int, string) {
	// The egg declares MAX_PLAYERS and QUERY_PORT variables. SERVER_PORT is not a
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

func (p *pteroPanel) serverExternal(ext string) (int, string) {
	if s, ok := p.servers[ext]; ok {
		return 200, p.serverJSON(s)
	}
	return 404, `{}`
}

func (p *pteroPanel) createServer(body map[string]any) (int, string) {
	deploy, _ := body["deploy"].(map[string]any)
	ranges, _ := deploy["port_range"].([]any)
	dedicated, _ := deploy["dedicated_ip"].(bool)

	skip := map[string]bool{}
	if dedicated {
		for _, a := range p.allocs {
			if a.Assigned {
				skip[a.IP] = true
			}
		}
	}
	var chosen *pteroAlloc
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
	s := &pteroServer{
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

func (p *pteroPanel) buildServer(sid int64, body map[string]any) (int, string) {
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
			s.AllocIDs = removeID(s.AllocIDs, id)
		}
	}
	return 200, p.serverJSON(s)
}

func (p *pteroPanel) startupServer(sid int64, body map[string]any) (int, string) {
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

func (p *pteroPanel) setSuspended(sid int64, v bool) {
	if s := p.byID[sid]; s != nil {
		s.Suspended = v
	}
}

func (p *pteroPanel) deleteServer(sid int64) (int, string) {
	s := p.byID[sid]
	if s == nil {
		return 404, `{}`
	}
	delete(p.byID, sid)
	delete(p.servers, s.ExternalID)
	return 204, ``
}

func (p *pteroPanel) serverJSON(s *pteroServer) string {
	var allocItems []string
	for _, id := range s.AllocIDs {
		if a := p.allocByID(id); a != nil {
			allocItems = append(allocItems, fmt.Sprintf(
				`{"attributes":{"id":%d,"ip":%q,"port":%d,"assigned":%t,"alias":""}}`,
				a.ID, a.IP, a.Port, a.Assigned))
		}
	}
	var envItems []string
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

func pteroServerID(ep string) int64 {
	return mustID(pathHead(strings.TrimPrefix(ep, "servers/")))
}
func mustID(s string) int64 { n, _ := strconv.ParseInt(pathHead(s), 10, 64); return n }
func contains(s []int64, v int64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
func removeID(s []int64, v int64) []int64 {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func pteroEnv(s *pteroServer) any {
	if s == nil {
		return "<nil server>"
	}
	return s.Environment
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runPtero(wasm []byte) {
	pteroMultiPort(wasm)
	pteroLifecycle(wasm)
	pteroDedicatedPort(wasm)
	pteroConfigOptions(wasm)
}

func pteroMultiPort(wasm []byte) {
	fmt.Println("── multi-port deploy (SERVER_PORT + QUERY_PORT co-located)")
	p := newPtero()
	plugin := loadPlugin(wasm, p.route)

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

	s := p.servers["1024"]
	assert("QUERY_PORT injected into environment", s != nil && s.Environment["QUERY_PORT"] == strconv.Itoa(qp), pteroEnv(s))
	assert("MAX_PLAYERS default override applied", s != nil && s.Environment["MAX_PLAYERS"] == "30", pteroEnv(s))
	assert("SERVER_PORT NOT set as env var", s != nil && s.Environment["SERVER_PORT"] == "", pteroEnv(s))
	assert("server has 2 allocations attached", s != nil && len(s.AllocIDs) == 2, s.AllocIDs)
}

func pteroLifecycle(wasm []byte) {
	fmt.Println("── suspend / unsuspend / update / terminate")
	p := newPtero()
	plugin := loadPlugin(wasm, p.route)
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
	assert("update succeeds", call(plugin, "UpdateService", upd)["success"] == true, nil)
	assert("update re-applied MAX_PLAYERS", p.servers["1024"].Environment["MAX_PLAYERS"] == "64", pteroEnv(p.servers["1024"]))

	assert("terminate", call(plugin, "TerminateService", args)["success"] == true, nil)
	assert("server removed", p.servers["1024"] == nil, nil)
	assert("suspend missing server fails gracefully", call(plugin, "SuspendService", args)["success"] == false, nil)
}

func pteroDedicatedPort(wasm []byte) {
	fmt.Println("── dedicated port pins the first port of the range")
	p := newPtero()
	plugin := loadPlugin(wasm, p.route)
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

func pteroConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newPtero()
	plugin := loadPlugin(wasm, p.route)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	assert("schema has fields", len(data) >= 15, len(data))
	found := false
	for _, f := range data {
		m, _ := f.(map[string]any)
		if m["key"] == "ports_ranges" {
			found = m["required"] == true
		}
	}
	assert("ports_ranges advertised & required", found, nil)
}
