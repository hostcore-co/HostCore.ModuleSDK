// HostCore Service Module — Pterodactyl
//
// This is a reference implementation of a HostCore service module. It manages
// game/application servers on a Pterodactyl panel and is modelled on the
// behaviour of the PteroSync WHMCS module: it does far more than naively
// creating a server. In particular it performs *intelligent multi-port
// allocation*, assigning every port a game requires (SERVER_PORT, QUERY_PORT,
// RCON_PORT, …) from configurable ranges, guaranteeing they all live on the
// same node IP, and wiring the assigned ports into the server's start-up
// environment automatically.
//
// A module script may only import the HostCore.ModuleSDK — no other Go
// packages beyond the standard library primitives the SDK re-exports. All
// outbound HTTP flows through the SDK's host functions, and all input/output
// is JSON exchanged with the Module Engine.
//
// # Input contract
//
// Every exported function receives a JSON argument document from the Engine
// with the following well-known fields:
//
//	{
//	  "domain":      "panel.example.com",   // panel host (no scheme)
//	  "api_key":     "ptla_xxx",            // Pterodactyl *application* API key
//	  "service_id":  "1024",                // store service id → panel external_id
//	  "password":    "optional-secret",     // desired panel password (deploy / change-password)
//	  "user":        { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
//
// The service_config array carries the values for the fields advertised by
// GetServiceConfigOptions (nest_id, egg_id, ports_ranges, …).
//
// # Output contract
//
// Functions return via sdk.Success(data) / sdk.Fail(message, data). Deploy and
// Update return a data document describing the provisioned server (ids, uuid,
// assigned ip/port and the full port map) so the store can persist it.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies the store's connection to the Pterodactyl
// panel and that the supplied application key is valid.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	args := sdk.GetArgs()
	p := newPanel(args)

	resp := p.get("nodes?per_page=1")
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the application API key.", resp.Body)
	}
	if !resp.OK {
		return sdk.Fail("Connection failed with status "+sdk.Itoa(resp.Status)+".", resp.Body)
	}

	nodes := sdk.GetBytes(resp.Body, "meta.pagination.total").Int()
	if nodes == 0 {
		return sdk.Fail("Authenticated successfully, but the panel has no nodes configured.", resp.Body)
	}

	return sdk.Success(sdk.NewJSON().
		Set("status", resp.Status).
		Set("nodes", nodes).
		Build())
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Nest","type":"number","key":"nest_id","description":"Pterodactyl nest that groups the server type.","regex":"^[0-9]+$","required":true},
		{"order":2,"label":"Egg","type":"number","key":"egg_id","description":"Egg that defines the application/game to run.","regex":"^[0-9]+$","required":true},
		{"order":3,"label":"Location","type":"number","key":"location_id","description":"Location the server will be deployed into.","regex":"^[0-9]+$","required":true},
		{"order":4,"label":"Server Name","type":"string","key":"server_name","description":"Display name of the server on the panel. Defaults to a generated name.","regex":"","required":false},
		{"order":5,"label":"CPU Limit (%)","type":"number","key":"cpu","description":"CPU limit as a percentage. 0 = unlimited.","regex":"^[0-9]+$","required":true},
		{"order":6,"label":"Memory","type":"number","key":"memory","description":"Memory limit. Unit is controlled by memory_unit (default MB). 0 = unlimited.","regex":"^[0-9]+$","required":true},
		{"order":7,"label":"Memory Unit","type":"string","key":"memory_unit","description":"MB or GB. Controls how the Memory value is interpreted.","regex":"^(MB|GB|mb|gb)$","required":false},
		{"order":8,"label":"Disk","type":"number","key":"disk","description":"Disk limit. Unit is controlled by disk_unit (default MB). 0 = unlimited.","regex":"^[0-9]+$","required":true},
		{"order":9,"label":"Disk Unit","type":"string","key":"disk_unit","description":"MB or GB. Controls how the Disk value is interpreted.","regex":"^(MB|GB|mb|gb)$","required":false},
		{"order":10,"label":"Swap","type":"number","key":"swap","description":"Swap limit. Unit is controlled by swap_unit (default MB).","regex":"^-?[0-9]+$","required":false},
		{"order":11,"label":"Swap Unit","type":"string","key":"swap_unit","description":"MB or GB. Controls how the Swap value is interpreted.","regex":"^(MB|GB|mb|gb)$","required":false},
		{"order":12,"label":"Block IO Weight","type":"number","key":"io","description":"Block IO weight (10-1000).","regex":"^[0-9]+$","required":false},
		{"order":13,"label":"CPU Pinning","type":"string","key":"threads","description":"Specific CPU cores this server may use, e.g. 0, 0-1,3. Blank = all cores.","regex":"^[0-9,-]*$","required":false},
		{"order":14,"label":"Databases","type":"number","key":"databases","description":"Number of databases the client may create.","regex":"^[0-9]+$","required":false},
		{"order":15,"label":"Backups","type":"number","key":"backups","description":"Number of backups the client may create.","regex":"^[0-9]+$","required":false},
		{"order":16,"label":"Allocations","type":"number","key":"allocations","description":"Additional allocations the client may add themselves.","regex":"^[0-9]+$","required":false},
		{"order":17,"label":"Port Ranges","type":"string","key":"ports_ranges","description":"JSON of named port ranges. SERVER_PORT is required. e.g. {\"SERVER_PORT\":\"25565-25669\",\"QUERY_PORT\":\"27015-27020\"}. Every port is placed on the same node IP.","regex":"","required":true},
		{"order":18,"label":"Default Variables","type":"string","key":"default_variables","description":"JSON of default egg variable overrides, e.g. {\"MAX_PLAYERS\":\"30\"}.","regex":"","required":false},
		{"order":19,"label":"Server Port Offset","type":"number","key":"server_port_offset","description":"Reserve an extra allocation at SERVER_PORT + offset (e.g. 1 for ARK). 0 disables.","regex":"^[0-9]+$","required":false},
		{"order":20,"label":"Dedicated IP","type":"boolean","key":"dedicated_ip","description":"Only deploy onto an IP with no other assigned allocations.","regex":"","required":false},
		{"order":21,"label":"Dedicated Port","type":"boolean","key":"dedicated_port","description":"Always assign the first port of the SERVER_PORT range, giving the client a fixed port.","regex":"","required":false},
		{"order":22,"label":"Docker Image","type":"string","key":"image","description":"Override the egg's Docker image (optional).","regex":"","required":false},
		{"order":23,"label":"Startup Command","type":"string","key":"startup","description":"Override the egg's start-up command (optional).","regex":"","required":false},
		{"order":24,"label":"Disable OOM Killer","type":"boolean","key":"oom_disabled","description":"Disable the out-of-memory killer for this server.","regex":"","required":false},
		{"order":25,"label":"Feature Limits","type":"string","key":"feature_limits","description":"JSON of additional feature-limit overrides merged into the server (optional).","regex":"","required":false}
	]`))
}

// DeployService provisions a new Pterodactyl server. It mirrors the PteroSync
// create flow: get-or-create the panel user, resolve the egg, create the
// server within the SERVER_PORT range, then (when the configuration requires
// more than one port) locate a set of free ports on a single IP, attach them
// and inject them into the start-up environment.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	p := newPanel(args)
	cfg, msg := loadConfig(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	externalID := serviceID(args)

	// Refuse to deploy twice over the same service id.
	if existing := p.get("servers/external/" + externalID); existing.Status == 200 {
		return sdk.Fail("A server already exists for this service.", nil)
	}

	// 1. Resolve (or create) the panel user for this client.
	userID, msg := p.getOrCreateUser(args.User(), args.Get("password").String())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	// 2. Load the egg (docker image, start-up command and variables).
	egg := p.get("nests/" + cfg.nestID + "/eggs/" + cfg.eggID + "?include=variables")
	if !egg.OK {
		return sdk.Fail("Failed to load egg "+cfg.eggID+" (status "+sdk.Itoa(egg.Status)+").", egg.Body)
	}
	eggAttr := sdk.GetBytes(egg.Body, "attributes")
	image := cfg.image
	if image == "" {
		image = eggAttr.Get("docker_image").String()
	}
	startup := cfg.startup
	if startup == "" {
		startup = eggAttr.Get("startup").String()
	}

	// 3. Build the environment from egg defaults + configured overrides.
	env := buildEnvironment(eggAttr.Get("relationships.variables.data"), cfg.defaultVars)

	// 4. Create the server. The panel assigns one allocation from the
	//    SERVER_PORT range; additional ports are attached afterwards.
	serverPortRange := cfg.ports.Get("SERVER_PORT").String()
	if serverPortRange == "" {
		return sdk.Fail("ports_ranges must contain a SERVER_PORT entry.", nil)
	}
	if cfg.dedicatedPort {
		serverPortRange = firstPortOf(serverPortRange)
	}

	payload := buildCreatePayload(createParams{
		name:       cfg.serverName(externalID),
		userID:     userID,
		nestID:     cfg.nestID,
		eggID:      cfg.eggID,
		image:      image,
		startup:    startup,
		externalID: externalID,
		locationID: cfg.locationID,
		portRange:  serverPortRange,
		dedicated:  cfg.dedicatedIP,
		limits:     cfg.limits(),
		features:   cfg.featureLimits(cfg.allocations),
		env:        env.build(),
	})

	created := p.post("servers?include=allocations", payload)
	if created.Status == 400 {
		return sdk.Fail("No node could satisfy the request — check locations, ports and capacity.", created.Body)
	}
	if created.Status != 201 {
		return sdk.Fail("Failed to create the server (status "+sdk.Itoa(created.Status)+").", created.Body)
	}

	srv := sdk.GetBytes(created.Body, "attributes")
	serverID := srv.Get("id").Int64()
	uuid := srv.Get("uuid").String()
	identifier := srv.Get("identifier").String()
	nodeID := srv.Get("node").Int64()
	defaultAllocID := srv.Get("allocation").Int64()
	ip, port := ipAndPort(srv.Get("relationships.allocations.data"), defaultAllocID)

	portMap := map[string]int{"SERVER_PORT": port}

	// 5. Attach any additional ports required by the configuration.
	if cfg.needsExtraPorts() {
		newIP, newPort, msg := p.attachPorts(attachParams{
			serverID:       serverID,
			nodeID:         nodeID,
			defaultAllocID: defaultAllocID,
			cfg:            cfg,
			egg:            eggAttr,
			env:            env,
			image:          image,
			startup:        startup,
			portMap:        portMap,
		})
		if msg != "" {
			// The server exists but is only partially configured; surface the
			// error with enough context for the store to reconcile/retry.
			return sdk.Fail(msg, sdk.NewJSON().
				Set("server_id", int(serverID)).
				Set("uuid", uuid).
				Build())
		}
		if newIP != "" {
			ip, port = newIP, newPort
		}
	}

	return sdk.Success(buildServerResult(serverResult{
		serverID:   serverID,
		uuid:       uuid,
		identifier: identifier,
		userID:     userID,
		ip:         ip,
		port:       port,
		ports:      portMap,
	}))
}

// SuspendService suspends the server bound to this service.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	return powerLifecycle("suspend", "suspend")
}

// UnsuspendService restores a suspended server.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	return powerLifecycle("unsuspend", "unsuspend")
}

// TerminateService permanently deletes the server bound to this service.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	args := sdk.GetArgs()
	p := newPanel(args)

	id, msg := p.serverIDByExternal(serviceID(args))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	resp := p.delete("servers/" + sdk.Itoa64(id))
	if resp.Status != 204 {
		return sdk.Fail("Failed to terminate the server (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService applies a package/plan change: it re-applies resource limits,
// feature limits and the egg's environment/start-up to the existing server.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	p := newPanel(args)
	cfg, msg := loadConfig(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	server := p.get("servers/external/" + serviceID(args) + "?include=allocations")
	if server.Status != 200 {
		return sdk.Fail("Failed to locate the server to update (status "+sdk.Itoa(server.Status)+").", server.Body)
	}
	srv := sdk.GetBytes(server.Body, "attributes")
	serverID := srv.Get("id").Int64()
	currentAlloc := srv.Get("allocation").Int64()

	// Re-apply build limits, preserving the current primary allocation.
	build := sdk.NewJSON().
		Set("allocation", int(currentAlloc)).
		Set("memory", cfg.memory).
		Set("swap", cfg.swap).
		Set("io", cfg.io).
		Set("cpu", cfg.cpu).
		Set("disk", cfg.disk).
		Set("threads", cfg.threads).
		Set("oom_disabled", cfg.oomDisabled).
		Set("feature_limits", cfg.featureLimits(cfg.allocations)).
		Build()

	buildResp := p.patch("servers/"+sdk.Itoa64(serverID)+"/build", build)
	if buildResp.Status != 200 {
		return sdk.Fail("Failed to update server limits (status "+sdk.Itoa(buildResp.Status)+").", buildResp.Body)
	}

	// Re-apply egg, image, start-up and environment.
	egg := p.get("nests/" + cfg.nestID + "/eggs/" + cfg.eggID + "?include=variables")
	if !egg.OK {
		return sdk.Fail("Failed to load egg "+cfg.eggID+" (status "+sdk.Itoa(egg.Status)+").", egg.Body)
	}
	eggAttr := sdk.GetBytes(egg.Body, "attributes")
	image := cfg.image
	if image == "" {
		image = eggAttr.Get("docker_image").String()
	}
	startup := cfg.startup
	if startup == "" {
		startup = eggAttr.Get("startup").String()
	}
	env := buildEnvironment(eggAttr.Get("relationships.variables.data"), cfg.defaultVars)

	startupResp := p.patch("servers/"+sdk.Itoa64(serverID)+"/startup", sdk.NewJSON().
		Set("egg", sdk.Atoi(cfg.eggID)).
		Set("image", image).
		Set("startup", startup).
		Set("environment", env.build()).
		Set("skip_scripts", false).
		Build())
	if startupResp.Status != 200 {
		return sdk.Fail("Failed to update server start-up (status "+sdk.Itoa(startupResp.Status)+").", startupResp.Body)
	}

	return sdk.Success(sdk.NewJSON().
		Set("server_id", int(serverID)).
		Set("uuid", srv.Get("uuid").String()).
		Build())
}

// ChangePassword updates the panel password of the client's user account.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	p := newPanel(args)

	password := args.Get("password").String()
	if password == "" {
		return sdk.Fail("The password cannot be empty.", nil)
	}

	user := p.get("users/external/" + args.User().ID)
	if user.Status != 200 {
		return sdk.Fail("Failed to locate the panel user (status "+sdk.Itoa(user.Status)+").", user.Body)
	}
	attr := sdk.GetBytes(user.Body, "attributes")

	resp := p.patch("users/"+sdk.Itoa64(attr.Get("id").Int64()), sdk.NewJSON().
		Set("username", attr.Get("username").String()).
		Set("email", attr.Get("email").String()).
		Set("first_name", attr.Get("first_name").String()).
		Set("last_name", attr.Get("last_name").String()).
		Set("password", password).
		Build())
	if resp.Status != 200 {
		return sdk.Fail("Failed to change the password (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	return sdk.Success(nil)
}

// powerLifecycle performs a suspend/unsuspend POST for the current service.
func powerLifecycle(action, verb string) int32 {
	args := sdk.GetArgs()
	p := newPanel(args)

	id, msg := p.serverIDByExternal(serviceID(args))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	resp := p.post("servers/"+sdk.Itoa64(id)+"/"+action, nil)
	if resp.Status != 204 {
		return sdk.Fail("Failed to "+verb+" the server (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// Panel client
// -----------------------------------------------------------------------------

// panel is a thin, typed wrapper over the Pterodactyl application API. It
// centralises the base URL and authentication headers so callers deal only in
// endpoints and bodies.
type panel struct {
	base string
	hdrs map[string]string
}

func newPanel(args sdk.Args) panel {
	return panel{
		base: "https://" + args.Get("domain").String() + "/api/application/",
		hdrs: pteroHeaders(args.Get("api_key").String()),
	}
}

func (p panel) get(endpoint string) sdk.HTTPResponse {
	return sdk.HTTPGet(p.base+endpoint, p.hdrs)
}

func (p panel) post(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPost(p.base+endpoint, p.hdrs, body)
}

func (p panel) patch(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPatch(p.base+endpoint, p.hdrs, body)
}

func (p panel) delete(endpoint string) sdk.HTTPResponse {
	return sdk.HTTPDelete(p.base+endpoint, p.hdrs)
}

// serverIDByExternal resolves a panel server id from a store service id. The
// returned message is non-empty on error.
func (p panel) serverIDByExternal(externalID string) (int64, string) {
	resp := p.get("servers/external/" + externalID)
	if resp.Status == 404 {
		return 0, "No server exists for this service."
	}
	if resp.Status != 200 {
		return 0, "Failed to locate the server (status " + sdk.Itoa(resp.Status) + ")."
	}
	return sdk.GetBytes(resp.Body, "attributes.id").Int64(), ""
}

// getOrCreateUser resolves the Pterodactyl user for a store user, creating one
// if necessary. It matches PteroSync's precedence: first by external_id, then
// by e-mail (adopting and re-linking any pre-existing account), and finally by
// creating a fresh account.
func (p panel) getOrCreateUser(u sdk.User, password string) (int64, string) {
	// 1. Fast path: already linked by external_id.
	if byExt := p.get("users/external/" + u.ID); byExt.Status == 200 {
		return sdk.GetBytes(byExt.Body, "attributes.id").Int64(), ""
	}

	// 2. Adopt an existing account with the same e-mail and re-link it.
	if u.Email != "" {
		search := p.get("users?filter[email]=" + urlEncode(u.Email))
		if search.OK {
			var matched sdk.Result
			var found bool
			sdk.GetBytes(search.Body, "data").ForEach(func(_, item sdk.Result) bool {
				if equalFold(item.Get("attributes.email").String(), u.Email) {
					matched = item
					found = true
					return false
				}
				return true
			})
			if found {
				id := matched.Get("attributes.id").Int64()
				relink := sdk.NewJSON().
					Set("username", matched.Get("attributes.username").String()).
					Set("email", matched.Get("attributes.email").String()).
					Set("first_name", matched.Get("attributes.first_name").String()).
					Set("last_name", matched.Get("attributes.last_name").String()).
					Set("external_id", u.ID)
				if password != "" {
					relink.Set("password", password)
				}
				p.patch("users/"+sdk.Itoa64(id), relink.Build()) // best-effort relink
				return id, ""
			}
		}
	}

	// 3. Create a brand-new account.
	create := sdk.NewJSON().
		Set("email", u.Email).
		Set("username", pteroUsername(u)).
		Set("first_name", orDefault(u.FirstName, "HostCore")).
		Set("last_name", orDefault(u.LastName, "Client")).
		Set("external_id", u.ID).
		Set("root_admin", false)
	if password != "" {
		create.Set("password", password)
	}

	resp := p.post("users", create.Build())
	if resp.Status != 201 && resp.Status != 200 {
		detail := sdk.GetBytes(resp.Body, "errors.0.detail").String()
		if detail == "" {
			detail = "status " + sdk.Itoa(resp.Status)
		}
		return 0, "Failed to create the panel user: " + detail
	}
	id := sdk.GetBytes(resp.Body, "attributes.id").Int64()
	if id == 0 {
		return 0, "Panel accepted the user but returned no id."
	}
	return id, ""
}

// nodeAllocations returns every allocation on a node, following pagination so
// large nodes are handled correctly.
func (p panel) nodeAllocations(nodeID int64) ([]allocation, string) {
	var out []allocation
	page := 1
	for {
		resp := p.get("nodes/" + sdk.Itoa64(nodeID) + "/allocations?per_page=100&page=" + sdk.Itoa(page))
		if !resp.OK {
			return nil, "Failed to read allocations for node " + sdk.Itoa64(nodeID) + " (status " + sdk.Itoa(resp.Status) + ")."
		}
		sdk.GetBytes(resp.Body, "data").ForEach(func(_, item sdk.Result) bool {
			a := item.Get("attributes")
			out = append(out, allocation{
				id:       a.Get("id").Int64(),
				ip:       a.Get("ip").String(),
				port:     a.Get("port").Int(),
				assigned: a.Get("assigned").Bool(),
			})
			return true
		})
		totalPages := sdk.GetBytes(resp.Body, "meta.pagination.total_pages").Int()
		if totalPages <= page {
			break
		}
		page++
	}
	if len(out) == 0 {
		return nil, "Node " + sdk.Itoa64(nodeID) + " has no allocations — create ports for it first."
	}
	return out, ""
}

// attachPorts locates a set of free ports satisfying the configured ranges on
// a single IP, attaches them to the server and injects them into the start-up
// environment. It returns the (possibly re-selected) primary ip/port.
func (p panel) attachPorts(a attachParams) (string, int, string) {
	allocs, msg := p.nodeAllocations(a.nodeID)
	if msg != "" {
		return "", 0, msg
	}

	// Determine the IP of the server's current default allocation; it anchors
	// dedicated-IP filtering. The default allocation already belongs to this
	// server, so treat it as available for (re)selection — this avoids
	// needless churn and lets a range with a single free port still resolve.
	var anchorIP string
	for i := range allocs {
		if allocs[i].id == a.defaultAllocID {
			anchorIP = allocs[i].ip
			allocs[i].assigned = false
			break
		}
	}

	ips, order := groupByIP(allocs, a.cfg.dedicatedIP, anchorIP)

	vars := a.cfg.portVariables(a.egg)
	found, ok := findPorts(vars, ips, order)
	if !ok {
		return "", 0, "Could not find a single IP with free ports for all of: " + joinVarNames(vars) + "."
	}

	// The re-selected SERVER_PORT becomes the primary allocation.
	primary := found["SERVER_PORT"]
	build := sdk.NewJSON().
		Set("allocation", int(primary.id)).
		Set("memory", a.cfg.memory).
		Set("swap", a.cfg.swap).
		Set("io", a.cfg.io).
		Set("cpu", a.cfg.cpu).
		Set("disk", a.cfg.disk).
		Set("threads", a.cfg.threads).
		Set("oom_disabled", a.cfg.oomDisabled)

	var addIDs, removeIDs []int64
	for _, v := range vars {
		f := found[v.name]
		addIDs = append(addIDs, f.id)
		a.portMap[v.name] = f.port
	}
	if primary.id != a.defaultAllocID {
		removeIDs = append(removeIDs, a.defaultAllocID)
	}

	build.Set("add_allocations", sdk.JSONInt64Array(addIDs))
	if len(removeIDs) > 0 {
		build.Set("remove_allocations", sdk.JSONInt64Array(removeIDs))
	}
	// The client may now hold more allocations than originally provisioned.
	build.Set("feature_limits", a.cfg.featureLimits(a.cfg.allocations+len(addIDs)))

	buildResp := p.patch("servers/"+sdk.Itoa64(a.serverID)+"/build?include=allocations", build.Build())
	if buildResp.Status != 200 {
		return "", 0, "Failed to attach allocations (status " + sdk.Itoa(buildResp.Status) + ")."
	}

	// Inject the assigned ports into the environment. SERVER_PORT is derived
	// from the primary allocation and is therefore not set as a variable.
	for _, v := range vars {
		if v.name == "SERVER_PORT" || v.synthetic {
			continue
		}
		a.env.set(v.name, sdk.Itoa(a.portMap[v.name]))
	}

	startupResp := p.patch("servers/"+sdk.Itoa64(a.serverID)+"/startup", sdk.NewJSON().
		Set("egg", int(a.egg.Get("id").Int64())).
		Set("image", a.image).
		Set("startup", a.startup).
		Set("environment", a.env.build()).
		Set("skip_scripts", false).
		Build())
	if startupResp.Status != 200 {
		return "", 0, "Failed to inject port variables into start-up (status " + sdk.Itoa(startupResp.Status) + ")."
	}

	updated := sdk.GetBytes(buildResp.Body, "attributes")
	ip, port := ipAndPort(updated.Get("relationships.allocations.data"), primary.id)
	return ip, port, ""
}

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

// deployConfig is the validated, normalised view of a service's configuration
// options, resolved once per call.
type deployConfig struct {
	nestID      string
	eggID       string
	locationID  string
	name        string
	image       string
	startup     string
	threads     string
	cpu         int
	memory      int
	disk        int
	swap        int
	io          int
	databases   int
	backups     int
	allocations int
	oomDisabled bool
	dedicatedIP bool

	dedicatedPort bool
	portOffset    int

	ports       sdk.Result // parsed ports_ranges object
	defaultVars sdk.Result // parsed default_variables object
	features    sdk.Result // parsed feature_limits object (may not exist)
}

// loadConfig reads and validates the service_config options. The returned
// message is non-empty on validation failure.
func loadConfig(args sdk.Args) (deployConfig, string) {
	c := deployConfig{
		nestID:      cfgStr(args, "nest_id"),
		eggID:       cfgStr(args, "egg_id"),
		locationID:  cfgStr(args, "location_id"),
		name:        cfgStr(args, "server_name"),
		image:       cfgStr(args, "image"),
		startup:     cfgStr(args, "startup"),
		threads:     validateThreads(cfgStr(args, "threads")),
		cpu:         sdk.Atoi(cfgStr(args, "cpu")),
		io:          sdk.Atoi(cfgStr(args, "io")),
		databases:   sdk.Atoi(cfgStr(args, "databases")),
		backups:     sdk.Atoi(cfgStr(args, "backups")),
		allocations: sdk.Atoi(cfgStr(args, "allocations")),
		oomDisabled: sdk.ParseBool(cfgStr(args, "oom_disabled")),
		dedicatedIP: sdk.ParseBool(cfgStr(args, "dedicated_ip")),

		dedicatedPort: sdk.ParseBool(cfgStr(args, "dedicated_port")),
		portOffset:    sdk.Atoi(cfgStr(args, "server_port_offset")),

		memory:      resolveUnit(cfgStr(args, "memory"), cfgStr(args, "memory_unit")),
		disk:        resolveUnit(cfgStr(args, "disk"), cfgStr(args, "disk_unit")),
		swap:        resolveUnit(cfgStr(args, "swap"), cfgStr(args, "swap_unit")),
		ports:       sdk.Parse(cfgStr(args, "ports_ranges")),
		defaultVars: sdk.Parse(cfgStr(args, "default_variables")),
		features:    sdk.Parse(cfgStr(args, "feature_limits")),
	}

	if c.nestID == "" || c.eggID == "" {
		return c, "nest_id and egg_id are required."
	}
	if c.locationID == "" {
		return c, "location_id is required."
	}
	if !c.ports.Get("SERVER_PORT").Exists() {
		return c, "ports_ranges must be valid JSON containing a SERVER_PORT range."
	}
	return c, ""
}

func (c deployConfig) serverName(externalID string) string {
	if c.name != "" {
		return c.name
	}
	return "hostcore_" + externalID
}

// limits returns the JSON limits object for server creation.
func (c deployConfig) limits() []byte {
	return sdk.NewJSON().
		Set("memory", c.memory).
		Set("swap", c.swap).
		Set("disk", c.disk).
		Set("io", c.io).
		Set("cpu", c.cpu).
		Set("threads", c.threads).
		Set("oom_disabled", c.oomDisabled).
		Build()
}

// featureLimits returns the JSON feature_limits object, merging any operator
// overrides supplied via the feature_limits config value.
func (c deployConfig) featureLimits(allocations int) []byte {
	j := sdk.NewJSON().
		Set("databases", c.databases).
		Set("allocations", allocations).
		Set("backups", c.backups)
	if c.features.IsObject() {
		c.features.ForEach(func(k, v sdk.Result) bool {
			j.Set(k.String(), v.Int())
			return true
		})
	}
	return j.Build()
}

// needsExtraPorts reports whether the configuration requires more than the
// single SERVER_PORT allocation the panel assigns at creation time.
func (c deployConfig) needsExtraPorts() bool {
	if c.portOffset > 0 {
		return true
	}
	extra := false
	c.ports.ForEach(func(k, _ sdk.Result) bool {
		if k.String() != "SERVER_PORT" {
			extra = true
			return false
		}
		return true
	})
	return extra
}

// portVariables returns the ordered list of port variables to allocate, with
// SERVER_PORT always first. Only variables recognised by the egg (or the
// special EXTRA_ALLOCATION / offset entries) are included.
func (c deployConfig) portVariables(egg sdk.Result) []portVar {
	// Names the egg actually understands, so we don't attach ports the
	// start-up can't consume.
	eggVars := map[string]bool{}
	egg.Get("relationships.variables.data").ForEach(func(_, item sdk.Result) bool {
		eggVars[item.Get("attributes.env_variable").String()] = true
		return true
	})

	serverRange := c.ports.Get("SERVER_PORT").String()
	if c.dedicatedPort {
		serverRange = firstPortOf(serverRange)
	}
	vars := []portVar{parsePortVar("SERVER_PORT", serverRange, false)}

	c.ports.ForEach(func(k, v sdk.Result) bool {
		name := k.String()
		if name == "SERVER_PORT" || name == "EXTRA_ALLOCATION" {
			return true
		}
		if eggVars[name] {
			vars = append(vars, parsePortVar(name, v.String(), false))
		}
		return true
	})

	// server_port_offset: reserve an additional allocation at SERVER_PORT +
	// offset. Modelled as a synthetic variable whose range is the offset span
	// of the SERVER_PORT range so it lands on the same IP.
	if c.portOffset > 0 {
		start, end, ok := sdk.ParsePortRange(serverRange)
		if ok {
			vars = append(vars, portVar{
				name:      "SERVER_PORT_OFFSET",
				start:     start + c.portOffset,
				end:       end + c.portOffset,
				synthetic: true,
			})
		}
	}

	// EXTRA_ALLOCATION: "start-end" or "start-end:N" reserves N nameless extra
	// allocations (no environment variable).
	if ea := c.ports.Get("EXTRA_ALLOCATION").String(); ea != "" {
		rng, times := parseExtraAllocation(ea)
		for i := 0; i < times; i++ {
			pv := parsePortVar("EXTRA_ALLOCATION_"+sdk.Itoa(i), rng, true)
			vars = append(vars, pv)
		}
	}
	return vars
}

// cfgStr reads a trimmed service_config value.
func cfgStr(args sdk.Args, key string) string {
	return sdk.TrimSpace(args.ServiceConfig(key))
}

// resolveUnit converts a numeric string to megabytes, honouring an optional
// "MB"/"GB" unit (default MB).
func resolveUnit(value, unit string) int {
	switch lowerUnit(unit) {
	case "gb":
		return sdk.GBToMB(value)
	default:
		return sdk.Atoi(value)
	}
}

// -----------------------------------------------------------------------------
// Allocation / port model
// -----------------------------------------------------------------------------

// allocation is a single node port allocation.
type allocation struct {
	id       int64
	ip       string
	port     int
	assigned bool
}

// portVar is a named port requirement with an inclusive [start,end] range.
// synthetic entries (offset/extra allocations) are attached but not written as
// environment variables.
type portVar struct {
	name      string
	start     int
	end       int
	synthetic bool
}

func parsePortVar(name, rng string, synthetic bool) portVar {
	start, end, _ := sdk.ParsePortRange(rng)
	return portVar{name: name, start: start, end: end, synthetic: synthetic}
}

// sortByPort orders allocations ascending by port with a simple in-place
// insertion sort. This avoids sort.Slice (and its reflection dependency),
// keeping the module small and portable across TinyGo and Go wasip1. Node
// allocation counts are small, so insertion sort is more than adequate.
func sortByPort(a []allocation) {
	for i := 1; i < len(a); i++ {
		cur := a[i]
		j := i - 1
		for j >= 0 && a[j].port > cur.port {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = cur
	}
}

// groupByIP groups allocations by IP, preserving first-seen order for
// determinism and sorting each IP's ports ascending. When dedicated is true,
// IPs that carry allocations already assigned to another server are dropped so
// the whole IP is reserved for this deployment.
func groupByIP(allocs []allocation, dedicated bool, anchorIP string) (map[string][]allocation, []string) {
	skip := map[string]bool{}
	if dedicated {
		for _, a := range allocs {
			if a.assigned && a.ip != anchorIP {
				skip[a.ip] = true
			}
		}
	}

	ips := map[string][]allocation{}
	var order []string
	for _, a := range allocs {
		if skip[a.ip] {
			continue
		}
		if _, ok := ips[a.ip]; !ok {
			order = append(order, a.ip)
		}
		ips[a.ip] = append(ips[a.ip], a)
	}
	for ip := range ips {
		sortByPort(ips[ip])
	}
	return ips, order
}

// findPorts searches each IP (in order) for a set of unassigned ports that
// satisfies every variable's range, one distinct allocation per variable. It
// returns the first fully-satisfiable IP's assignments.
func findPorts(vars []portVar, ips map[string][]allocation, order []string) (map[string]allocation, bool) {
	for _, ip := range order {
		ports := ips[ip]
		used := map[int64]bool{}
		result := map[string]allocation{}
		ok := true

		for _, v := range vars {
			matched := false
			for _, a := range ports {
				if a.assigned || used[a.id] {
					continue
				}
				if a.port >= v.start && a.port <= v.end {
					result[v.name] = a
					used[a.id] = true
					matched = true
					break
				}
			}
			if !matched {
				ok = false
				break
			}
		}

		if ok {
			return result, true
		}
	}
	return nil, false
}

// -----------------------------------------------------------------------------
// Environment building
// -----------------------------------------------------------------------------

// kvJSON is an insertion-ordered string→string map that serialises to a JSON
// object with deterministic key order (important for reproducible payloads and
// tests).
type kvJSON struct {
	keys []string
	vals []string
}

func newKV() *kvJSON { return &kvJSON{} }

func (e *kvJSON) set(k, v string) {
	for i, kk := range e.keys {
		if kk == k {
			e.vals[i] = v
			return
		}
	}
	e.keys = append(e.keys, k)
	e.vals = append(e.vals, v)
}

func (e *kvJSON) build() []byte {
	j := sdk.NewJSON()
	for i := range e.keys {
		j.Set(e.keys[i], e.vals[i])
	}
	return j.Build()
}

// buildEnvironment seeds the environment from the egg's variable defaults,
// overriding any variable that appears in default_variables.
func buildEnvironment(eggVars sdk.Result, defaults sdk.Result) *kvJSON {
	env := newKV()
	eggVars.ForEach(func(_, item sdk.Result) bool {
		attr := item.Get("attributes")
		key := attr.Get("env_variable").String()
		if key == "" {
			return true
		}
		value := attr.Get("default_value").String()
		if defaults.IsObject() {
			if ov := defaults.Get(key); ov.Exists() {
				value = ov.String()
			}
		}
		env.set(key, value)
		return true
	})
	return env
}

// -----------------------------------------------------------------------------
// Payload / result builders
// -----------------------------------------------------------------------------

type createParams struct {
	name       string
	userID     int64
	nestID     string
	eggID      string
	image      string
	startup    string
	externalID string
	locationID string
	portRange  string
	dedicated  bool
	limits     []byte
	features   []byte
	env        []byte
}

// buildCreatePayload assembles the Pterodactyl server-creation body, including
// the deploy block that constrains the initial allocation to the SERVER_PORT
// range and (optionally) a dedicated IP.
func buildCreatePayload(c createParams) []byte {
	deploy := sdk.NewJSON().
		Set("locations", sdk.JSONInt64Array([]int64{sdk.Atoi64(c.locationID)})).
		Set("dedicated_ip", c.dedicated).
		Set("port_range", sdk.JSONStringArray(splitCSV(c.portRange))).
		Build()

	return sdk.NewJSON().
		Set("name", c.name).
		Set("user", int(c.userID)).
		Set("nest", sdk.Atoi(c.nestID)).
		Set("egg", sdk.Atoi(c.eggID)).
		Set("docker_image", c.image).
		Set("startup", c.startup).
		Set("external_id", c.externalID).
		Set("start_on_completion", true).
		Set("environment", c.env).
		Set("limits", c.limits).
		Set("feature_limits", c.features).
		Set("deploy", deploy).
		Build()
}

type attachParams struct {
	serverID       int64
	nodeID         int64
	defaultAllocID int64
	cfg            deployConfig
	egg            sdk.Result
	env            *kvJSON
	image          string
	startup        string
	portMap        map[string]int
}

type serverResult struct {
	serverID   int64
	uuid       string
	identifier string
	userID     int64
	ip         string
	port       int
	ports      map[string]int
}

func buildServerResult(r serverResult) []byte {
	ports := sdk.NewJSON()
	// Deterministic ordering: SERVER_PORT first, then the rest as inserted.
	if v, ok := r.ports["SERVER_PORT"]; ok {
		ports.Set("SERVER_PORT", v)
	}
	for name, port := range r.ports {
		if name != "SERVER_PORT" {
			ports.Set(name, port)
		}
	}

	return sdk.NewJSON().
		Set("server_id", int(r.serverID)).
		Set("uuid", r.uuid).
		Set("identifier", r.identifier).
		Set("user_id", int(r.userID)).
		Set("ip", r.ip).
		Set("port", r.port).
		Set("address", r.ip+":"+sdk.Itoa(r.port)).
		Set("ports", ports.Build()).
		Build()
}

// ipAndPort returns the ip and port of the allocation matching allocID within
// an allocations relationship array.
func ipAndPort(allocations sdk.Result, allocID int64) (string, int) {
	var ip string
	var port int
	allocations.ForEach(func(_, item sdk.Result) bool {
		attr := item.Get("attributes")
		if attr.Get("id").Int64() == allocID {
			ip = attr.Get("ip").String()
			if alias := attr.Get("alias").String(); alias != "" {
				ip = alias
			}
			port = attr.Get("port").Int()
			return false
		}
		return true
	})
	return ip, port
}

// -----------------------------------------------------------------------------
// Small helpers
// -----------------------------------------------------------------------------

// pteroHeaders returns the standard headers required by the Pterodactyl API.
func pteroHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}
}

func serviceID(args sdk.Args) string {
	if id := sdk.TrimSpace(args.Get("service_id").String()); id != "" {
		return id
	}
	return args.User().ID
}

// pteroUsername derives a panel-safe username from the client's e-mail, falling
// back to a random string. Pterodactyl usernames must be alphanumeric.
func pteroUsername(u sdk.User) string {
	var b []byte
	for i := 0; i < len(u.Email); i++ {
		c := u.Email[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
		if c == '@' {
			break
		}
	}
	if len(b) < 3 {
		return "u" + sdk.RandomString(11)
	}
	return string(b) + sdk.RandomString(4)
}

// validateThreads mirrors the panel's CPU-pinning validation: only digits,
// commas and dashes are permitted; anything else yields an empty (all-cores)
// value.
func validateThreads(threads string) string {
	if threads == "" {
		return ""
	}
	for i := 0; i < len(threads); i++ {
		c := threads[i]
		if !((c >= '0' && c <= '9') || c == ',' || c == '-') {
			return ""
		}
	}
	return threads
}

// firstPortOf collapses a "start-end" range to "start-start" so a fixed port is
// always assigned (dedicated port).
func firstPortOf(rng string) string {
	start, _, ok := sdk.ParsePortRange(rng)
	if !ok {
		return rng
	}
	s := sdk.Itoa(start)
	return s + "-" + s
}

// parseExtraAllocation parses an EXTRA_ALLOCATION value of the form
// "start-end" or "start-end:N", returning the range and the repeat count.
func parseExtraAllocation(s string) (string, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			n := sdk.Atoi(s[i+1:])
			if n < 1 {
				n = 1
			}
			return s[:i], n
		}
	}
	return s, 1
}

// splitCSV splits a comma-separated list, trimming each element and dropping
// empties. Used for the deploy port_range which may list multiple ranges.
func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := sdk.TrimSpace(s[start:i])
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

// joinVarNames renders a comma-separated list of variable names for error
// messages.
func joinVarNames(vars []portVar) string {
	var out string
	for i, v := range vars {
		if i > 0 {
			out += ", "
		}
		out += v.name
	}
	return out
}

// urlEncode percent-encodes a query-parameter value. Kept minimal (no net/url)
// so the compiled module stays small; it encodes everything outside the
// RFC 3986 unreserved set.
func urlEncode(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b = append(b, c)
			continue
		}
		b = append(b, '%', upperhex[c>>4], upperhex[c&0x0f])
	}
	return string(b)
}

// equalFold reports whether a and b are equal under ASCII case-folding.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func lowerUnit(s string) string {
	b := []byte(sdk.TrimSpace(s))
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
