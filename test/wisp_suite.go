// Mock WISP panel and scenarios. WISP resolves additional-port allocation
// server-side, so the suite asserts on the request payloads the module sends
// (ULID-safe ids, null-for-blank limits, translated additional_ports) as well
// as the lifecycle outcomes.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type wispServer struct {
	ID          string
	UUID        string
	Identifier  string
	ExternalID  string
	Allocation  string
	Suspended   bool
	Environment map[string]string
}

type wispPanel struct {
	users     map[string]string // external_id -> user id
	usersByID map[string]map[string]string
	nextUser  int
	servers   map[string]*wispServer // external_id -> server
	byID      map[string]*wispServer
	nextSrv   int

	lastCreate   map[string]any
	lastBuild    map[string]any
	lastStartup  map[string]any
	lastTemplate map[string]any
	lastUserPUT  map[string]any
}

func newWisp() *wispPanel {
	return &wispPanel{
		users:     map[string]string{},
		usersByID: map[string]map[string]string{},
		nextUser:  100,
		servers:   map[string]*wispServer{},
		byID:      map[string]*wispServer{},
		nextSrv:   5000,
	}
}

func (p *wispPanel) route(method, rawURL string, body map[string]any) (int, string) {
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
	case method == "GET" && ep == "nodes":
		return 200, `{"meta":{"pagination":{"total":3}},"data":[{"attributes":{"id":1}}]}`
	case method == "GET" && strings.HasPrefix(ep, "users/external/"):
		return p.userExternal(pathHead(strings.TrimPrefix(ep, "users/external/")))
	case method == "GET" && ep == "users":
		return p.usersBySearch(q.Get("search"))
	case method == "POST" && ep == "users":
		return p.createUser(body)
	case method == "PATCH" && strings.HasPrefix(ep, "users/"):
		p.lastUserPUT = body
		return 200, p.userJSON(pathHead(strings.TrimPrefix(ep, "users/")))
	case method == "GET" && strings.HasPrefix(ep, "eggs/"):
		return p.egg()
	case method == "GET" && strings.HasPrefix(ep, "servers/external/"):
		return p.serverExternal(pathHead(strings.TrimPrefix(ep, "servers/external/")))
	case method == "GET" && ep == "servers":
		return p.serversBySearch(q.Get("search"))
	case method == "GET" && strings.HasPrefix(ep, "servers/"):
		return p.serverByID(wispServerID(ep))
	case method == "POST" && ep == "servers":
		return p.createServer(body)
	case method == "PATCH" && strings.HasSuffix(ep, "/startup"):
		p.lastStartup = body
		return 200, `{"attributes":{}}`
	case method == "PATCH" && strings.HasSuffix(ep, "/server-template"):
		p.lastTemplate = body
		return 200, `{"attributes":{}}`
	case method == "PATCH" && strings.HasSuffix(ep, "/build"):
		p.lastBuild = body
		return 200, `{"attributes":{}}`
	case method == "POST" && strings.HasSuffix(ep, "/suspend"):
		p.setSuspended(wispServerID(ep), true)
		return 204, ``
	case method == "POST" && strings.HasSuffix(ep, "/unsuspend"):
		p.setSuspended(wispServerID(ep), false)
		return 204, ``
	case method == "DELETE" && strings.HasPrefix(ep, "servers/"):
		return p.deleteServer(wispServerID(ep))
	}
	return 404, `{}`
}

func (p *wispPanel) userExternal(id string) (int, string) {
	if uid, ok := p.users[id]; ok {
		return 200, p.userJSON(uid)
	}
	return 404, `{}`
}

func (p *wispPanel) usersBySearch(email string) (int, string) {
	for uid, u := range p.usersByID {
		if strings.EqualFold(u["email"], email) {
			return 200, fmt.Sprintf(`{"meta":{"pagination":{"total":1}},"data":[%s]}`, p.userJSON(uid))
		}
	}
	return 200, `{"meta":{"pagination":{"total":0}},"data":[]}`
}

func (p *wispPanel) createUser(body map[string]any) (int, string) {
	// WISP ids are opaque strings; return a ULID-style id to exercise the
	// module's string-id handling end-to-end.
	uid := fmt.Sprintf("user_%d", p.nextUser)
	p.nextUser++
	p.usersByID[uid] = map[string]string{
		"email":      str(body["email"]),
		"first_name": str(body["first_name"]),
		"last_name":  str(body["last_name"]),
	}
	if ext := str(body["external_id"]); ext != "" {
		p.users[ext] = uid
	}
	return 201, p.userJSON(uid)
}

func (p *wispPanel) userJSON(uid string) string {
	u := p.usersByID[uid]
	return fmt.Sprintf(`{"attributes":{"id":%q,"email":%q,"first_name":%q,"last_name":%q}}`,
		uid, u["email"], u["first_name"], u["last_name"])
}

func (p *wispPanel) egg() (int, string) {
	return 200, `{"attributes":{
		"docker_image":"quay.io/wisp/games:source",
		"startup":"./run",
		"relationships":{"variables":{"data":[
			{"attributes":{"env_variable":"MAX_PLAYERS","default_value":"20"}},
			{"attributes":{"env_variable":"QUERY_PORT","default_value":"0"}},
			{"attributes":{"env_variable":"RCON_PORT","default_value":"0"}}
		]}}
	}}`
}

func (p *wispPanel) serverExternal(ext string) (int, string) {
	if s, ok := p.servers[ext]; ok {
		return 200, p.serverJSON(s)
	}
	return 404, `{}`
}

func (p *wispPanel) serversBySearch(term string) (int, string) {
	for _, s := range p.byID {
		if s.UUID == term {
			return 200, fmt.Sprintf(`{"data":[%s]}`, p.serverJSON(s))
		}
	}
	return 200, `{"data":[]}`
}

func (p *wispPanel) serverByID(id string) (int, string) {
	s := p.byID[id]
	if s == nil {
		return 404, `{}`
	}
	return 200, p.serverJSON(s)
}

// seedServer registers a server this module never deployed: an imported service
// whose panel record carries the external id it held in the source system.
func (p *wispPanel) seedServer(externalID string) *wispServer {
	sid := fmt.Sprintf("srv_%d", p.nextSrv)
	p.nextSrv++
	s := &wispServer{
		ID: sid, UUID: fmt.Sprintf("uuid-%s", sid), Identifier: fmt.Sprintf("id%s", sid),
		ExternalID: externalID, Allocation: "alloc_1", Environment: map[string]string{},
	}
	p.servers[externalID] = s
	p.byID[sid] = s
	return s
}

func (p *wispPanel) createServer(body map[string]any) (int, string) {
	p.lastCreate = body
	sid := fmt.Sprintf("srv_%d", p.nextSrv)
	p.nextSrv++
	ext := str(body["external_id"])
	s := &wispServer{
		ID: sid, UUID: fmt.Sprintf("uuid-%s", sid), Identifier: fmt.Sprintf("id%s", sid),
		ExternalID: ext, Allocation: "alloc_1", Environment: map[string]string{},
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

func (p *wispPanel) setSuspended(id string, v bool) {
	if s := p.byID[id]; s != nil {
		s.Suspended = v
	}
}

func (p *wispPanel) deleteServer(id string) (int, string) {
	s := p.byID[id]
	if s == nil {
		return 404, `{}`
	}
	delete(p.byID, id)
	delete(p.servers, s.ExternalID)
	return 204, ``
}

func (p *wispPanel) serverJSON(s *wispServer) string {
	var envItems []string
	for k, v := range s.Environment {
		envItems = append(envItems, fmt.Sprintf("%q:%q", k, v))
	}
	return fmt.Sprintf(`{"attributes":{
		"id":%q,"uuid":%q,"identifier":%q,"external_id":%q,"user":"user_100","allocation":%q,
		"container":{"image":"quay.io/wisp/games:source","startup_command":"./run","environment":{%s}},
		"relationships":{"allocations":{"data":[{"attributes":{"id":%q,"ip":"1.2.3.4","port":25565,"alias":""}}]}}
	}}`, s.ID, s.UUID, s.Identifier, s.ExternalID, s.Allocation, strings.Join(envItems, ","), s.Allocation)
}

func wispServerID(ep string) string {
	return pathHead(strings.TrimPrefix(ep, "servers/"))
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runWisp(wasm []byte) {
	wispDeploy(wasm)
	wispStringID(wasm)
	wispLifecycle(wasm)
	wispImported(wasm)
	wispConfigOptions(wasm)
}

func wispDeploy(wasm []byte) {
	fmt.Println("── deploy with additional ports, tags and null-inheriting limits")
	p := newWisp()
	plugin := loadPlugin(wasm, p.route)

	args := baseArgs(strings.Join([]string{
		cfg("egg_id", "5"), cfg("location_id", "2"),
		cfg("cpu", "100"), cfg("memory", ""), cfg("disk", "10240"),
		cfg("dedicated_ip", "on"), cfg("force_outgoing_ip", "on"),
		cfg("tags", "premium, ssd"),
		cfg("additional_ports", `{"1":"QUERY_PORT","2":"RCON_PORT:27015-27020","3":"NONE"}`),
		cfg("additional_port_fail_mode", "stop"),
	}, ","))

	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)

	c := p.lastCreate
	assert("egg sent as JSON number (numeric id)", c["egg"] == float64(5), c["egg"])
	_, userIsString := c["user"].(string)
	assert("user sent as JSON string (ULID id)", userIsString && c["user"] == "user_100", c["user"])

	limits, _ := c["limits"].(map[string]any)
	assert("blank memory sent as null (template inheritance)", limits["memory"] == nil, limits["memory"])
	assert("cpu sent as number", limits["cpu"] == float64(100), limits["cpu"])

	deploy, _ := c["deploy"].(map[string]any)
	assert("dedicated_ip forwarded", deploy["dedicated_ip"] == true, deploy["dedicated_ip"])
	tags, _ := deploy["tags"].([]any)
	assert("tags parsed and forwarded", len(tags) == 2 && str(tags[0]) == "premium" && str(tags[1]) == "ssd", tags)
	assert("force_outgoing_ip forwarded", c["force_outgoing_ip"] == true, c["force_outgoing_ip"])
	assert("additional_port_fail_mode forwarded", deploy["additional_port_fail_mode"] == "stop", deploy["additional_port_fail_mode"])

	ap, _ := deploy["additional_ports"].([]any)
	assert("3 additional ports translated", len(ap) == 3, ap)
	assert("QUERY_PORT → offset entry (offset 1)", findPort(ap, "QUERY_PORT", "offset", 1, 0, 0), ap)
	assert("RCON_PORT → range entry (27015-27020)", findPort(ap, "RCON_PORT", "range", 0, 27015, 27020), ap)
	assert("NONE → null-variable offset entry", findNullPort(ap, "offset", 3), ap)

	data, _ := res["data"].(map[string]any)
	assert("result reports address from primary allocation", data["address"] == "1.2.3.4:25565", data["address"])
}

func wispStringID(wasm []byte) {
	fmt.Println("── opaque (ULID) egg id is preserved as a JSON string")
	p := newWisp()
	plugin := loadPlugin(wasm, p.route)
	args := baseArgs(strings.Join([]string{
		cfg("egg_id", "egg_01k9xyz"), cfg("location_id", "loc_01k9abc"),
		cfg("cpu", "150"),
	}, ","))
	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)
	assert("string egg id preserved", p.lastCreate["egg"] == "egg_01k9xyz", p.lastCreate["egg"])
	deploy, _ := p.lastCreate["deploy"].(map[string]any)
	locs, _ := deploy["locations"].([]any)
	assert("string location id preserved in array", len(locs) == 1 && locs[0] == "loc_01k9abc", locs)
}

func wispLifecycle(wasm []byte) {
	fmt.Println("── suspend / unsuspend / update (egg-first ordering) / change password / terminate")
	p := newWisp()
	plugin := loadPlugin(wasm, p.route)
	base := []string{cfg("egg_id", "5"), cfg("location_id", "2"), cfg("cpu", "100")}
	args := baseArgs(strings.Join(base, ","))

	assert("deploy", call(plugin, "DeployService", args)["success"] == true, nil)
	assert("suspend", call(plugin, "SuspendService", args)["success"] == true, nil)
	assert("server marked suspended", p.servers["1024"].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", args)["success"] == true, nil)
	assert("server marked unsuspended", p.servers["1024"].Suspended == false, nil)

	upd := baseArgs(strings.Join([]string{
		cfg("egg_id", "5"), cfg("location_id", "2"),
		cfg("memory", "2048"), cfg("server_template_id", "tmpl_1"),
		cfg("MAX_PLAYERS", "64"),
	}, ","))
	assert("update succeeds", call(plugin, "UpdateService", upd)["success"] == true, nil)
	build, _ := p.lastBuild["feature_limits"].(map[string]any)
	assert("update pushed memory 2048", p.lastBuild["memory"] == float64(2048), p.lastBuild["memory"])
	assert("update sent feature_limits", build != nil, p.lastBuild)
	assert("blank allocations inherited as null", build["allocations"] == nil, build["allocations"])
	env, _ := p.lastStartup["environment"].(map[string]any)
	assert("startup pushed before build (captured)", p.lastStartup["egg"] == float64(5), p.lastStartup["egg"])
	assert("egg-variable override applied on update", env["MAX_PLAYERS"] == "64", env)
	assert("template relinked with resync", p.lastTemplate["server_template"] == "tmpl_1" && p.lastTemplate["resync"] == true, p.lastTemplate)

	assert("change password", call(plugin, "ChangePassword", args)["success"] == true, nil)
	assert("password patch sent, no username", p.lastUserPUT["password"] == "secret123" && p.lastUserPUT["username"] == nil, p.lastUserPUT)

	assert("terminate", call(plugin, "TerminateService", args)["success"] == true, nil)
	assert("server removed", p.servers["1024"] == nil, nil)
}

func wispImported(wasm []byte) {
	fmt.Println("── imported server resolves by remote binding or source id")
	p := newWisp()
	plugin := loadPlugin(wasm, p.route)
	srv := p.seedServer("7788")

	args := func(identity string) string {
		return `{"domain":"panel.test","api_key":"key_test","service_id":"1024",` + identity +
			`"user":{"id":"77","email":"Alice@Example.com","first_name":"Alice","last_name":"Ng"},
			"service_config":[]}`
	}

	assert("suspend by source id", call(plugin, "SuspendService", args(`"external_id":"7788",`))["success"] == true, nil)
	assert("imported server marked suspended", srv.Suspended == true, nil)

	assert("unsuspend by panel server id", call(plugin, "UnsuspendService", args(fmt.Sprintf(`"remote_id":%q,`, srv.ID)))["success"] == true, nil)
	assert("imported server marked unsuspended", srv.Suspended == false, nil)

	assert("suspend by panel uuid", call(plugin, "SuspendService", args(fmt.Sprintf(`"remote_uuid":%q,`, srv.UUID)))["success"] == true, nil)
	assert("imported server marked suspended again", srv.Suspended == true, nil)

	assert("unsuspend without any binding fails gracefully", call(plugin, "UnsuspendService", args(``))["success"] == false, nil)

	assert("terminate by source id", call(plugin, "TerminateService", args(`"external_id":"7788",`))["success"] == true, nil)
	assert("imported server removed", p.servers["7788"] == nil, nil)
}

func wispConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newWisp()
	plugin := loadPlugin(wasm, p.route)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	assert("schema has fields", len(data) >= 15, len(data))
	eggRequired := false
	for _, f := range data {
		m, _ := f.(map[string]any)
		if m["key"] == "egg_id" {
			eggRequired = m["required"] == true
		}
	}
	assert("egg_id advertised & required", eggRequired, nil)
}

// findPort reports whether entries contains an offset/range entry for the given
// variable with the expected numbers.
func findPort(entries []any, variable, typ string, offset, from, to int) bool {
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if str(m["variable"]) != variable || m["type"] != typ {
			continue
		}
		if typ == "offset" {
			return toInt(m["offset"]) == offset
		}
		return toInt(m["from"]) == from && toInt(m["to"]) == to
	}
	return false
}

func findNullPort(entries []any, typ string, offset int) bool {
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m["variable"] == nil && m["type"] == typ && toInt(m["offset"]) == offset {
			return true
		}
	}
	return false
}
