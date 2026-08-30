// Mock Convoy panel and scenarios. Drives the convoy module through the server
// lifecycle: user resolution by e-mail, address allocation from the node pool,
// the byte-scaled build limits, the plan change that has to carry existing
// addresses through, and the panel password reset.
package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type convoyUser struct {
	ID       int
	Name     string
	Email    string
	Password string
	Admin    bool
}

type convoyAddress struct {
	ID       int
	Type     string
	Address  string
	ServerID int
}

type convoyServer struct {
	UUID      string
	NodeID    int
	UserID    int
	Hostname  string
	Name      string
	Suspended bool
	Addresses []int
	Limits    map[string]any
}

type convoyPanel struct {
	users     map[int]*convoyUser
	nextUser  int
	servers   map[string]*convoyServer
	nextUUID  int
	addresses map[int]*convoyAddress

	lastCreate map[string]any
	lastBuild  map[string]any
	lastUser   map[string]any
	perPage    []string
	ssoTokens  int
}

func newConvoy() *convoyPanel {
	p := &convoyPanel{
		users:     map[int]*convoyUser{},
		nextUser:  40,
		servers:   map[string]*convoyServer{},
		nextUUID:  1,
		addresses: map[int]*convoyAddress{},
	}
	// Four free IPv4 and two free IPv6 in node 4417's pool.
	for i := 1; i <= 4; i++ {
		p.addresses[i] = &convoyAddress{ID: i, Type: "ipv4", Address: fmt.Sprintf("198.51.100.%d", i)}
	}
	for i := 5; i <= 6; i++ {
		p.addresses[i] = &convoyAddress{ID: i, Type: "ipv6", Address: fmt.Sprintf("2001:db8::%d", i)}
	}
	return p
}

func (p *convoyPanel) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	ep := u.Path[strings.Index(u.Path, "/api/application/")+len("/api/application/"):]
	seg := strings.Split(ep, "/")
	q := u.Query()

	switch {
	case method == "GET" && ep == "nodes":
		return 200, `{"data":[{"id":4417,"name":"us-southeast"}]}`

	case method == "GET" && ep == "users":
		email := q.Get("filter[email]")
		rows := make([]string, 0)
		for _, usr := range p.users {
			// Convoy's filter is a partial match, which the module must not
			// take at face value.
			if email == "" || strings.Contains(strings.ToLower(usr.Email), strings.ToLower(email)) {
				rows = append(rows, usr.json())
			}
		}
		return 200, `{"data":[` + strings.Join(rows, ",") + `]}`

	case method == "POST" && len(seg) == 3 && seg[0] == "users" && seg[2] == "generate-sso-token":
		if p.users[atoiSafe(seg[1])] == nil {
			return 404, `{"message":"user not found"}`
		}
		p.ssoTokens++
		return 200, fmt.Sprintf(`{"data":{"user_id":%s,"token":"tok-%d"}}`, seg[1], p.ssoTokens)

	case method == "POST" && ep == "users":
		id := p.nextUser
		p.nextUser++
		p.users[id] = &convoyUser{
			ID: id, Name: str(body["name"]), Email: str(body["email"]),
			Password: str(body["password"]), Admin: body["root_admin"] == true,
		}
		p.lastUser = body
		return 201, `{"data":` + p.users[id].json() + `}`

	case len(seg) == 2 && seg[0] == "users":
		usr := p.users[atoiSafe(seg[1])]
		if usr == nil {
			return 404, `{"message":"user not found"}`
		}
		if method == "GET" {
			return 200, `{"data":` + usr.json() + `}`
		}
		if method == "PUT" {
			p.lastUser = body
			usr.Name = str(body["name"])
			if pw := str(body["password"]); pw != "" {
				usr.Password = pw
			}
			return 200, `{"data":` + usr.json() + `}`
		}
		return 405, `{"message":"method not allowed"}`

	case method == "GET" && len(seg) == 3 && seg[0] == "nodes" && pathHead(seg[2]) == "addresses":
		want := atoiSafe(q.Get("per_page"))
		kind := q.Get("filter[type]")
		p.perPage = append(p.perPage, kind+":"+q.Get("per_page"))
		rows := make([]string, 0)
		for i := 1; i <= len(p.addresses); i++ {
			a := p.addresses[i]
			if a == nil || a.Type != kind || a.ServerID != 0 || len(rows) >= want {
				continue
			}
			rows = append(rows, a.json())
		}
		return 200, `{"data":[` + strings.Join(rows, ",") + `]}`

	case method == "POST" && ep == "servers":
		p.lastCreate = body
		uuid := fmt.Sprintf("d45309ad-9c9e-4ea7-b386-%012d", p.nextUUID)
		p.nextUUID++
		limits, _ := body["limits"].(map[string]any)
		srv := &convoyServer{
			UUID: uuid, NodeID: toInt(body["node_id"]), UserID: toInt(body["user_id"]),
			Hostname: str(body["hostname"]), Name: str(body["name"]), Limits: limits,
		}
		if ids, ok := limits["address_ids"].([]any); ok {
			for _, raw := range ids {
				id := toInt(raw)
				srv.Addresses = append(srv.Addresses, id)
				p.addresses[id].ServerID = 1
			}
		}
		p.servers[uuid] = srv
		return 201, `{"data":` + srv.json(p) + `}`

	case method == "GET" && len(seg) == 2 && seg[0] == "servers":
		srv := p.servers[seg[1]]
		if srv == nil {
			return 404, `{"message":"server not found"}`
		}
		return 200, `{"data":` + srv.json(p) + `}`

	case method == "PATCH" && len(seg) == 4 && seg[0] == "servers" && seg[3] == "build":
		srv := p.servers[seg[1]]
		if srv == nil {
			return 404, `{"message":"server not found"}`
		}
		p.lastBuild = body
		srv.Addresses = nil
		if ids, ok := body["address_ids"].([]any); ok {
			for _, raw := range ids {
				id := toInt(raw)
				srv.Addresses = append(srv.Addresses, id)
				p.addresses[id].ServerID = 1
			}
		}
		return 200, `{"data":` + srv.json(p) + `}`

	case method == "POST" && len(seg) == 4 && seg[0] == "servers" && seg[2] == "settings":
		srv := p.servers[seg[1]]
		if srv == nil {
			return 404, `{"message":"server not found"}`
		}
		srv.Suspended = seg[3] == "suspend"
		return 204, ``

	case method == "DELETE" && len(seg) == 2 && seg[0] == "servers":
		if p.servers[seg[1]] == nil {
			return 404, `{"message":"server not found"}`
		}
		delete(p.servers, seg[1])
		return 204, ``
	}
	return 404, `{"message":"not found"}`
}

func (u *convoyUser) json() string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"email":%q,"root_admin":%t,"servers_count":0}`, u.ID, u.Name, u.Email, u.Admin)
}

func (a *convoyAddress) json() string {
	return fmt.Sprintf(`{"id":%d,"type":%q,"address":%q,"cidr":24,"gateway":"198.51.100.1","server_id":null}`, a.ID, a.Type, a.Address)
}

func (s *convoyServer) json(p *convoyPanel) string {
	v4, v6 := make([]string, 0), make([]string, 0)
	for _, id := range s.Addresses {
		a := p.addresses[id]
		row := fmt.Sprintf(`{"id":%d,"type":%q,"address":%q}`, a.ID, a.Type, a.Address)
		if a.Type == "ipv4" {
			v4 = append(v4, row)
		} else {
			v6 = append(v6, row)
		}
	}
	return fmt.Sprintf(`{"id":%q,"uuid":%q,"node_id":%d,"user_id":%d,"hostname":%q,"name":%q,"status":"installing","limits":{"addresses":{"ipv4":[%s],"ipv6":[%s]}}}`,
		s.UUID[:8], s.UUID, s.NodeID, s.UserID, s.Hostname, s.Name, strings.Join(v4, ","), strings.Join(v6, ","))
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

func runConvoy(wasm []byte) {
	convoyLifecycle(wasm)
	convoyUserMatching(wasm)
	convoyAddressExhaustion(wasm)
	convoyPasswords(wasm)
	convoyConfigOptions(wasm)
}

func convoyArgs(uuid, extra string) string {
	remote := ""
	if uuid != "" {
		remote = `"remote_id":"` + uuid + `",`
	}
	return `{
		"domain":"convoy.test",
		"api_key":"key_test",
		` + remote + `
		"service_id":"1024",
		"user":{"id":"77","email":"Alice@Example.com","first_name":"Alice","last_name":"Ng"},
		"service_config":[` + extra + `]
	}`
}

func convoyPlan() string {
	return strings.Join([]string{
		cfg("node_id", "4417"), cfg("template_uuid", "d176b498-87e8-421f-a958-048ef15ef199"),
		cfg("cpu", "3"), cfg("memory", "4096"), cfg("disk", "51200"),
		cfg("bandwidth", "102400"), cfg("snapshots", "2"), cfg("backups", "5"),
		cfg("ipv4", "2"), cfg("ipv6", "1"), cfg("hostname", "vps.example.com"),
	}, ",")
}

func convoyLifecycle(wasm []byte) {
	fmt.Println("── connection / create / suspend / unsuspend / plan change / terminate")
	p := newConvoy()
	plugin := loadPlugin(wasm, p.route)

	assert("test connection", call(plugin, "TestModuleConnection", convoyArgs("", ""))["success"] == true, nil)

	res := call(plugin, "DeployService", convoyArgs("", convoyPlan()))
	assert("deploy succeeds", res["success"] == true, res)

	assert("user created for the customer", len(p.users) == 1, p.users)
	assert("panel user is not an admin", p.lastUser["root_admin"] == false, p.lastUser)
	assert("create sent the node", toInt(p.lastCreate["node_id"]) == 4417, p.lastCreate["node_id"])
	assert("create sent the template", p.lastCreate["template_uuid"] == "d176b498-87e8-421f-a958-048ef15ef199", p.lastCreate["template_uuid"])
	assert("create asks Convoy to build the VM", p.lastCreate["should_create_server"] == true, p.lastCreate)
	assert("create starts the VM by default", p.lastCreate["start_on_completion"] == true, p.lastCreate)
	assert("vmid left for Convoy to assign", p.lastCreate["vmid"] == nil, p.lastCreate["vmid"])
	assert("server name fits Convoy's 40-character limit", len(str(p.lastCreate["name"])) <= 40, p.lastCreate["name"])

	limits, _ := p.lastCreate["limits"].(map[string]any)
	assert("cpu forwarded", toInt(limits["cpu"]) == 3, limits["cpu"])
	assert("memory scaled to bytes", toInt64(limits["memory"]) == 4096*1024*1024, limits["memory"])
	assert("disk scaled to bytes (past a 32-bit int)", toInt64(limits["disk"]) == 51200*1024*1024, limits["disk"])
	assert("bandwidth scaled to bytes", toInt64(limits["bandwidth"]) == 102400*1024*1024, limits["bandwidth"])
	assert("snapshots forwarded", toInt(limits["snapshots"]) == 2, limits["snapshots"])
	assert("backups forwarded", toInt(limits["backups"]) == 5, limits["backups"])

	ids, _ := limits["address_ids"].([]any)
	assert("two IPv4 and one IPv6 drawn from the pool", len(ids) == 3, ids)
	assert("addresses requested per family", strings.Join(p.perPage, " ") == "ipv4:2 ipv6:1", p.perPage)

	data, _ := res["data"].(map[string]any)
	uuid := str(data["server_id"])
	assert("deploy returns the server UUID", p.servers[uuid] != nil, data["server_id"])
	assert("uuid also reported for the remote binding", data["uuid"] == uuid, data["uuid"])
	assert("primary IPv4 reported as the address", data["ip"] == "198.51.100.1", data["ip"])
	creds, _ := data["credentials"].(map[string]any)
	assert("root credentials reported", creds["username"] == "root" && creds["password"] != "", data["credentials"])

	lc := convoyArgs(uuid, convoyPlan())
	assert("suspend", call(plugin, "SuspendService", lc)["success"] == true, nil)
	assert("server suspended", p.servers[uuid].Suspended == true, nil)
	assert("unsuspend", call(plugin, "UnsuspendService", lc)["success"] == true, nil)
	assert("server unsuspended", p.servers[uuid].Suspended == false, nil)

	fmt.Println("── a plan change keeps the addresses the server already holds")
	upgraded := strings.Replace(convoyPlan(), cfg("ipv4", "2"), cfg("ipv4", "3"), 1)
	upgraded = strings.Replace(upgraded, cfg("memory", "4096"), cfg("memory", "8192"), 1)
	assert("plan change", call(plugin, "UpdateService", convoyArgs(uuid, upgraded))["success"] == true, nil)
	assert("memory raised in bytes", toInt64(p.lastBuild["memory"]) == 8192*1024*1024, p.lastBuild["memory"])
	assert("bandwidth limit carried as bytes", toInt64(p.lastBuild["bandwidth_limit"]) == 102400*1024*1024, p.lastBuild["bandwidth_limit"])
	assert("snapshot limit carried", toInt(p.lastBuild["snapshot_limit"]) == 2, p.lastBuild["snapshot_limit"])
	built, _ := p.lastBuild["address_ids"].([]any)
	assert("held addresses kept and one IPv4 added", len(built) == 4, built)
	assert("only the shortfall drawn", p.perPage[len(p.perPage)-1] == "ipv4:1", p.perPage)

	assert("terminate", call(plugin, "TerminateService", lc)["success"] == true, nil)
	assert("server removed", p.servers[uuid] == nil, nil)
	assert("terminate is idempotent (404 → success)", call(plugin, "TerminateService", lc)["success"] == true, nil)

	assert("deploy without a node fails", call(plugin, "DeployService", convoyArgs("", cfg("cpu", "1")))["success"] == false, nil)
	assert("lifecycle call without a UUID fails", call(plugin, "SuspendService", convoyArgs("", ""))["success"] == false, nil)

	withAction := func(a string) string {
		return strings.Replace(convoyArgs(uuid, ""), `"service_id":"1024"`, `"service_id":"1024","action":"`+a+`"`, 1)
	}
	action := call(plugin, "ServiceAction", withAction("restart"))
	assert("power actions report themselves unsupported", action["success"] == false, action)
	actionData, _ := action["data"].(map[string]any)
	assert("unsupported carries the documented code", actionData["code"] == "unsupported", action["data"])

	fmt.Println("── single sign-on mints a fresh link on every click")
	sso := call(plugin, "ServiceAction", withAction("sso"))
	assert("sso succeeds", sso["success"] == true, sso)
	ssoData, _ := sso["data"].(map[string]any)
	assert("sso returns an authenticate URL carrying the token",
		ssoData["url"] == "https://convoy.test/authenticate?token=tok-1", ssoData["url"])

	again := call(plugin, "ServiceAction", withAction("sso"))
	againData, _ := again["data"].(map[string]any)
	assert("a second click mints a second token", againData["url"] != ssoData["url"], againData["url"])
	assert("both clicks reached Convoy", p.ssoTokens == 2, p.ssoTokens)
}

// Convoy filters users by a partial e-mail match, so a lookup can return an
// account that merely contains the address asked for.
func convoyUserMatching(wasm []byte) {
	fmt.Println("── an existing user is reused, a near-miss is not")
	p := newConvoy()
	p.users[7] = &convoyUser{ID: 7, Name: "Someone Else", Email: "notalice@example.com"}
	plugin := loadPlugin(wasm, p.route)

	res := call(plugin, "DeployService", convoyArgs("", convoyPlan()))
	assert("deploy succeeds", res["success"] == true, res)
	assert("the near-miss account was not reused", toInt(p.lastCreate["user_id"]) != 7, p.lastCreate["user_id"])
	assert("a user was created for the real address", len(p.users) == 2, p.users)

	p2 := newConvoy()
	p2.users[9] = &convoyUser{ID: 9, Name: "Alice Ng", Email: "alice@example.com"}
	plugin2 := loadPlugin(wasm, p2.route)
	res = call(plugin2, "DeployService", convoyArgs("", convoyPlan()))
	assert("deploy succeeds", res["success"] == true, res)
	assert("the existing account is reused across e-mail case", toInt(p2.lastCreate["user_id"]) == 9, p2.lastCreate["user_id"])
	assert("no duplicate user created", len(p2.users) == 1, p2.users)
}

func convoyAddressExhaustion(wasm []byte) {
	fmt.Println("── a pool that cannot cover the plan fails the deploy")
	p := newConvoy()
	plugin := loadPlugin(wasm, p.route)

	greedy := strings.Replace(convoyPlan(), cfg("ipv4", "2"), cfg("ipv4", "9"), 1)
	res := call(plugin, "DeployService", convoyArgs("", greedy))
	assert("deploy fails", res["success"] == false, res)
	assert("failure names the shortfall", strings.Contains(str(res["error"]), "unallocated ipv4"), res["error"])
	assert("no server was built", len(p.servers) == 0, p.servers)
}

func convoyPasswords(wasm []byte) {
	fmt.Println("── generated passwords meet Convoy's complexity rules")
	p := newConvoy()
	plugin := loadPlugin(wasm, p.route)

	// No addresses are drawn, so the node's pool cannot run dry across the run
	// and every deploy reaches the point of minting a password.
	plan := strings.Replace(convoyPlan(), cfg("ipv4", "2"), cfg("ipv4", "0"), 1)
	plan = strings.Replace(plan, cfg("ipv6", "1"), cfg("ipv6", "0"), 1)

	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		res := call(plugin, "DeployService", convoyArgs("", plan))
		assert("deploy "+strconv.Itoa(i)+" succeeds", res["success"] == true, res)
		data, _ := res["data"].(map[string]any)
		creds, _ := data["credentials"].(map[string]any)
		pw := str(creds["password"])
		assert("password "+strconv.Itoa(i)+" satisfies the panel's policy", convoyPasswordOK(pw), pw)
		seen[pw] = true
	}
	assert("every generated password differs", len(seen) == 6, len(seen))

	fmt.Println("── the panel password reset preserves the account's other fields")
	p2 := newConvoy()
	p2.users[9] = &convoyUser{ID: 9, Name: "Alice Ng", Email: "alice@example.com", Password: "old"}
	plugin2 := loadPlugin(wasm, p2.route)
	res := call(plugin2, "ChangePassword", convoyArgs("", convoyPlan()))
	assert("change password", res["success"] == true, res)
	assert("account name preserved", p2.lastUser["name"] == "Alice Ng", p2.lastUser)
	assert("account e-mail preserved", p2.lastUser["email"] == "alice@example.com", p2.lastUser)
	data, _ := res["data"].(map[string]any)
	assert("new password returned and applied", p2.users[9].Password == str(data["password"]), p2.users[9].Password)
	assert("new password satisfies the policy", convoyPasswordOK(str(data["password"])), data["password"])
}

// convoyPasswordOK mirrors Convoy's rule: 8-50 characters spanning a lower-case
// letter, an upper-case letter, a digit and a symbol.
func convoyPasswordOK(pw string) bool {
	if len(pw) < 8 || len(pw) > 50 {
		return false
	}
	var lower, upper, digit, symbol bool
	for _, c := range pw {
		switch {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		case strings.ContainsRune("!@#$%^&*", c):
			symbol = true
		}
	}
	return lower && upper && digit && symbol
}

func convoyConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	p := newConvoy()
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
	assert("node, template and resources required",
		required["node_id"] && required["template_uuid"] && required["cpu"] && required["memory"] && required["disk"], required)
}
