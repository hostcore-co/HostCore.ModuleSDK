// HostCore Service Module — WISP
//
// This is a reference HostCore service module for the WISP game panel
// (https://wisp.gg), a Pterodactyl derivative. It is a sibling to the
// Pterodactyl example module and exports the same lifecycle, but speaks WISP's
// API and provisioning model. The notable differences from Pterodactyl are:
//
//   - Eggs are addressed flatly as eggs/<id> (there is no nest in the path).
//   - Users are searched with users?search=<email>.
//   - WISP v2 resource IDs may be opaque strings (ULIDs such as "user_01k…")
//     rather than integers, so IDs are emitted as-is and never blindly cast.
//   - Blank numeric limits are sent as null so the panel inherits them from an
//     attached server template (rather than forcing a zero).
//   - Additional-port allocation is resolved by the panel: the module forwards
//     an additional_ports specification and the panel binds the primary plus
//     additional allocations server-side. There is no client-side port hunting.
//   - Extra features: force_outgoing_ip, node tags, server templates, packs and
//     separate backup size/count limits.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "domain":      "panel.example.com",   // panel host (no scheme)
//	  "api_key":     "wisp_xxx",            // WISP application API key
//	  "service_id":  "1024",                // store service id → panel external_id
//	  "password":    "optional-secret",     // desired panel password (deploy / change-password)
//	  "user":        { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
//
// Egg-variable overrides are read from service_config by the variable's own
// environment key (e.g. a "MAX_PLAYERS" entry overrides the MAX_PLAYERS egg
// variable), mirroring WISP's WHMCS configurable-option behaviour.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// wispModuleVersion is reported to the panel via the X-Wisp-Module-Version
// header, which WISP uses to gate version-specific API behaviour.
const wispModuleVersion = "1.0.0"

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that the panel is reachable and the supplied
// application key authenticates. It requests a lightweight, non-sensitive
// endpoint purely to exercise the credentials; the response payload is not
// inspected.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	p := newPanel(sdk.GetArgs())

	resp := p.get("nodes?per_page=1")
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the application API key.", resp.Body)
	}
	if !resp.OK {
		return sdk.Fail("Connection failed with status "+sdk.Itoa(resp.Status)+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("status", resp.Status).Build())
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Egg","type":"string","key":"egg_id","description":"ID of the egg the server runs. Accepts a numeric id or a WISP string id.","regex":"","required":true},
		{"order":2,"label":"Location","type":"string","key":"location_id","description":"ID of the location to deploy into.","regex":"","required":true},
		{"order":3,"label":"Server Name","type":"string","key":"server_name","description":"Display name of the server on the panel. Defaults to a generated name.","regex":"","required":false},
		{"order":4,"label":"CPU Limit (%)","type":"number","key":"cpu","description":"CPU limit as a percentage. Blank inherits from the server template.","regex":"^[0-9]*$","required":false},
		{"order":5,"label":"Memory (MB)","type":"number","key":"memory","description":"Memory limit in MB. Blank inherits from the server template.","regex":"^[0-9]*$","required":false},
		{"order":6,"label":"Disk (MB)","type":"number","key":"disk","description":"Disk limit in MB. Blank inherits from the server template.","regex":"^[0-9]*$","required":false},
		{"order":7,"label":"Swap (MB)","type":"number","key":"swap","description":"Swap limit in MB. Blank inherits from the server template.","regex":"^-?[0-9]*$","required":false},
		{"order":8,"label":"Block IO Weight","type":"number","key":"io","description":"Block IO weight (10-1000).","regex":"^[0-9]*$","required":false},
		{"order":9,"label":"Databases","type":"number","key":"databases","description":"Number of databases the client may create.","regex":"^[0-9]*$","required":false},
		{"order":10,"label":"Allocations","type":"number","key":"allocations","description":"Additional allocations the client may add themselves.","regex":"^[0-9]*$","required":false},
		{"order":11,"label":"Backup Size Limit (MB)","type":"number","key":"backup_megabytes_limit","description":"Total backup storage the server may use, in MB.","regex":"^[0-9]*$","required":false},
		{"order":12,"label":"Backup Count Limit","type":"number","key":"backup_count_limit","description":"Number of backups the server may create.","regex":"^[0-9]*$","required":false},
		{"order":13,"label":"Port Range","type":"string","key":"port_range","description":"Comma-separated port ranges for the primary allocation, e.g. 25565-25570,25580-25590.","regex":"","required":false},
		{"order":14,"label":"Additional Ports","type":"string","key":"additional_ports","description":"JSON object keyed by offset mapping to an egg variable, optionally with a range. e.g. {\"1\":\"QUERY_PORT\",\"2\":\"RCON_PORT:27015-27020\",\"3\":\"NONE\"}. Resolved by the panel.","regex":"","required":false},
		{"order":15,"label":"Additional Port Failure Mode","type":"string","key":"additional_port_fail_mode","description":"continue: deploy even if additional ports can't be satisfied; stop: fail the deploy.","regex":"^(continue|stop)$","required":false},
		{"order":16,"label":"Node Tags","type":"string","key":"tags","description":"Comma-separated node tag slugs required for automatic deployment, in addition to the egg's own.","regex":"","required":false},
		{"order":17,"label":"Dedicated IP","type":"boolean","key":"dedicated_ip","description":"Only deploy onto a dedicated IP.","regex":"","required":false},
		{"order":18,"label":"Force Outgoing IP","type":"boolean","key":"force_outgoing_ip","description":"Force the server to use its primary allocation IP for outgoing connections (required by some Source-engine games).","regex":"","required":false},
		{"order":19,"label":"Disable OOM Killer","type":"boolean","key":"oom_disabled","description":"Disable the out-of-memory killer for this server.","regex":"","required":false},
		{"order":20,"label":"Pack","type":"string","key":"pack_id","description":"ID of the pack to install with (optional).","regex":"","required":false},
		{"order":21,"label":"Docker Image","type":"string","key":"image","description":"Override the egg's Docker image (optional).","regex":"","required":false},
		{"order":22,"label":"Startup Command","type":"string","key":"startup","description":"Override the egg's start-up command (optional).","regex":"","required":false},
		{"order":23,"label":"Server Template","type":"string","key":"server_template_id","description":"Identifier of the WISP server template this product maps to (optional). Blank limits inherit from it.","regex":"","required":false}
	]`))
}

// DeployService provisions a new WISP server. It resolves (or creates) the
// panel user, loads the egg, and creates the server. Additional-port
// allocation, when configured, is resolved by the panel.
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

	if existing := p.get("servers/external/" + externalID); existing.Status == 200 {
		return sdk.Fail("A server already exists for this service.", nil)
	}

	userID, msg := p.getOrCreateUser(args.User(), args.Get("password").String())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	egg := p.get("eggs/" + cfg.eggID + "?include=variables")
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

	env := deployEnvironment(args, eggAttr.Get("relationships.variables.data"))

	payload := buildCreatePayload(cfg, createParams{
		name:       cfg.serverName(externalID),
		userID:     userID,
		image:      image,
		startup:    startup,
		externalID: externalID,
		env:        env,
	})

	created := p.post("servers?include=allocations", payload)
	if created.Status == 400 {
		return sdk.Fail("No node could satisfy the request — check location, ports, tags and capacity.", created.Body)
	}
	if created.Status != 201 && created.Status != 200 {
		return sdk.Fail("Failed to create the server (status "+sdk.Itoa(created.Status)+").", created.Body)
	}
	if detail := sdk.GetBytes(created.Body, "errors.0.detail").String(); detail != "" {
		return sdk.Fail("Failed to create the server: "+detail+".", created.Body)
	}

	srv := sdk.GetBytes(created.Body, "attributes")
	result := sdk.NewJSON().
		Set("server_id", srv.Get("id").Bytes()).
		Set("uuid", srv.Get("uuid").String()).
		Set("identifier", srv.Get("identifier").String()).
		Set("user_id", wispID(userID))

	if ip, port, ok := allocationAddress(srv.Get("relationships.allocations.data"), srv.Get("allocation").String()); ok {
		result.Set("ip", ip).Set("port", port).Set("address", ip+":"+sdk.Itoa(port))
	}
	return sdk.Success(result.Build())
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

	resp := p.delete("servers/" + id)
	if resp.Status != 204 {
		return sdk.Fail("Failed to terminate the server (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService applies a package/plan change. The order is deliberate: the
// egg/start-up is pushed first (a package change may swap the egg), the server
// template is relinked so blank limits can inherit from it, and the build
// limits are pushed last.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	p := newPanel(args)
	cfg, msg := loadConfig(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	server := p.get("servers/external/" + serviceID(args))
	if server.Status != 200 {
		return sdk.Fail("Failed to locate the server to update (status "+sdk.Itoa(server.Status)+").", server.Body)
	}
	srv := sdk.GetBytes(server.Body, "attributes")
	serverID := srv.Get("id").String()

	egg := p.get("eggs/" + cfg.eggID + "?include=variables")
	if !egg.OK {
		return sdk.Fail("Failed to load egg "+cfg.eggID+" (status "+sdk.Itoa(egg.Status)+").", egg.Body)
	}
	eggAttr := sdk.GetBytes(egg.Body, "attributes")
	image := cfg.image
	if image == "" {
		image = srv.Get("container.image").String()
	}
	startup := cfg.startup
	if startup == "" {
		startup = srv.Get("container.startup_command").String()
	}

	startupBody := sdk.NewJSON().
		Set("egg", wispID(cfg.eggID)).
		Set("image", image).
		Set("startup", startup).
		Set("environment", updateEnvironment(args, eggAttr.Get("relationships.variables.data"), srv.Get("container.environment"))).
		Set("skip_scripts", false)
	if cfg.packID != "" {
		startupBody.Set("pack", wispID(cfg.packID))
	}
	if r := p.patch("servers/"+serverID+"/startup", startupBody.Build()); r.Status != 200 {
		return sdk.Fail("Failed to update server start-up (status "+sdk.Itoa(r.Status)+").", r.Body)
	}

	// The template relink is best-effort: panels without the templates feature
	// return an error, which must not fail an otherwise valid package change.
	hasTemplate := cfg.serverTemplate != ""
	tmpl := sdk.NewJSON().Set("resync", hasTemplate)
	if hasTemplate {
		tmpl.Set("server_template", cfg.serverTemplate)
	} else {
		tmpl.Set("server_template", nil)
	}
	p.patch("servers/"+serverID+"/server-template", tmpl.Build())

	build := sdk.NewJSON().
		Set("allocation", srv.Get("allocation").Bytes()).
		Set("memory", cfg.memory.value()).
		Set("swap", cfg.swap.value()).
		Set("io", cfg.io.value()).
		Set("cpu", cfg.cpu.value()).
		Set("disk", cfg.disk.value()).
		Set("oom_disabled", cfg.oomDisabled).
		Set("force_outgoing_ip", cfg.forceOutgoingIP).
		Set("feature_limits", cfg.featureLimits()).
		Build()
	if r := p.patch("servers/"+serverID+"/build", build); r.Status != 200 {
		return sdk.Fail("Failed to update server limits (status "+sdk.Itoa(r.Status)+").", r.Body)
	}

	return sdk.Success(sdk.NewJSON().
		Set("server_id", srv.Get("id").Bytes()).
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

	resp := p.patch("users/"+attr.Get("id").String(), sdk.NewJSON().
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

func powerLifecycle(action, verb string) int32 {
	args := sdk.GetArgs()
	p := newPanel(args)

	id, msg := p.serverIDByExternal(serviceID(args))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	resp := p.post("servers/"+id+"/"+action, nil)
	if resp.Status != 204 {
		return sdk.Fail("Failed to "+verb+" the server (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// Panel client
// -----------------------------------------------------------------------------

// panel is a thin, typed wrapper over the WISP application API. It centralises
// the base URL and authentication headers so callers deal only in endpoints
// and bodies.
type panel struct {
	base string
	hdrs map[string]string
}

func newPanel(args sdk.Args) panel {
	return panel{
		base: "https://" + args.Get("domain").String() + "/api/application/",
		hdrs: wispHeaders(args.Get("api_key").String()),
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

// serverIDByExternal resolves a panel server id from a store service id. The id
// is returned as a string to preserve WISP's opaque identifiers. The returned
// message is non-empty on error.
func (p panel) serverIDByExternal(externalID string) (string, string) {
	resp := p.get("servers/external/" + externalID)
	if resp.Status == 404 {
		return "", "No server exists for this service."
	}
	if resp.Status != 200 {
		return "", "Failed to locate the server (status " + sdk.Itoa(resp.Status) + ")."
	}
	return sdk.GetBytes(resp.Body, "attributes.id").String(), ""
}

// getOrCreateUser resolves the WISP user for a store user, creating one if
// necessary: first by external_id, then by e-mail (adopting and re-linking any
// pre-existing account), and finally by creating a fresh account. The user id
// is returned as a string to preserve WISP's opaque identifiers.
func (p panel) getOrCreateUser(u sdk.User, password string) (string, string) {
	if byExt := p.get("users/external/" + u.ID); byExt.Status == 200 {
		return sdk.GetBytes(byExt.Body, "attributes.id").String(), ""
	}

	if u.Email != "" {
		search := p.get("users?search=" + sdk.URLEncode(u.Email))
		if search.OK {
			var matched sdk.Result
			var found bool
			sdk.GetBytes(search.Body, "data").ForEach(func(_, item sdk.Result) bool {
				if sdk.EqualFold(item.Get("attributes.email").String(), u.Email) {
					matched = item
					found = true
					return false
				}
				return true
			})
			if found {
				id := matched.Get("attributes.id").String()
				relink := sdk.NewJSON().
					Set("email", matched.Get("attributes.email").String()).
					Set("first_name", matched.Get("attributes.first_name").String()).
					Set("last_name", matched.Get("attributes.last_name").String()).
					Set("external_id", u.ID)
				if password != "" {
					relink.Set("password", password)
				}
				p.patch("users/"+id, relink.Build()) // best-effort relink
				return id, ""
			}
		}
	}

	create := sdk.NewJSON().
		Set("email", u.Email).
		Set("first_name", orDefault(u.FirstName, "Unknown")).
		Set("last_name", orDefault(u.LastName, "User")).
		Set("external_id", u.ID)
	if password != "" {
		create.Set("password", password)
	}

	resp := p.post("users", create.Build())
	if resp.Status != 201 && resp.Status != 200 {
		detail := sdk.GetBytes(resp.Body, "errors.0.detail").String()
		if detail == "" {
			detail = "status " + sdk.Itoa(resp.Status)
		}
		return "", "Failed to create the panel user: " + detail
	}
	id := sdk.GetBytes(resp.Body, "attributes.id").String()
	if id == "" {
		return "", "Panel accepted the user but returned no id."
	}
	return id, ""
}

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

// deployConfig is the validated, normalised view of a service's configuration
// options, resolved once per call.
type deployConfig struct {
	eggID          string
	locationID     string
	name           string
	image          string
	startup        string
	packID         string
	serverTemplate string
	tags           string
	portRange      string
	additionalRaw  string
	failMode       string

	cpu       limit
	memory    limit
	disk      limit
	swap      limit
	io        limit
	databases limit
	allocations,
	backupMB,
	backupCount limit

	dedicatedIP     bool
	forceOutgoingIP bool
	oomDisabled     bool
}

func loadConfig(args sdk.Args) (deployConfig, string) {
	c := deployConfig{
		eggID:          cfgStr(args, "egg_id"),
		locationID:     cfgStr(args, "location_id"),
		name:           cfgStr(args, "server_name"),
		image:          cfgStr(args, "image"),
		startup:        cfgStr(args, "startup"),
		packID:         cfgStr(args, "pack_id"),
		serverTemplate: cfgStr(args, "server_template_id"),
		tags:           cfgStr(args, "tags"),
		portRange:      cfgStr(args, "port_range"),
		additionalRaw:  cfgStr(args, "additional_ports"),
		failMode:       cfgStr(args, "additional_port_fail_mode"),

		cpu:         limit(cfgStr(args, "cpu")),
		memory:      limit(cfgStr(args, "memory")),
		disk:        limit(cfgStr(args, "disk")),
		swap:        limit(cfgStr(args, "swap")),
		io:          limit(cfgStr(args, "io")),
		databases:   limit(cfgStr(args, "databases")),
		allocations: limit(cfgStr(args, "allocations")),
		backupMB:    limit(cfgStr(args, "backup_megabytes_limit")),
		backupCount: limit(cfgStr(args, "backup_count_limit")),

		dedicatedIP:     sdk.ParseBool(cfgStr(args, "dedicated_ip")),
		forceOutgoingIP: sdk.ParseBool(cfgStr(args, "force_outgoing_ip")),
		oomDisabled:     sdk.ParseBool(cfgStr(args, "oom_disabled")),
	}

	if c.eggID == "" {
		return c, "egg_id is required."
	}
	if c.locationID == "" {
		return c, "location_id is required."
	}
	return c, ""
}

func (c deployConfig) serverName(externalID string) string {
	if c.name != "" {
		return c.name
	}
	return "hostcore_" + externalID
}

// featureLimits returns the JSON feature_limits object. Blank limits are sent
// as null so the panel inherits them from an attached server template.
func (c deployConfig) featureLimits() []byte {
	return sdk.NewJSON().
		Set("databases", c.databases.value()).
		Set("allocations", c.allocations.value()).
		Set("backup_megabytes_limit", c.backupMB.value()).
		Set("backup_count_limit", c.backupCount.value()).
		Build()
}

// limits returns the JSON limits object for server creation.
func (c deployConfig) limits() []byte {
	return sdk.NewJSON().
		Set("memory", c.memory.value()).
		Set("swap", c.swap.value()).
		Set("io", c.io.value()).
		Set("cpu", c.cpu.value()).
		Set("disk", c.disk.value()).
		Build()
}

// limit is a raw configuration string for a numeric limit whose blank value
// carries meaning: an empty limit is emitted as JSON null so the panel inherits
// it from a server template, rather than being forced to zero.
type limit string

// value returns the JSON encoding input for this limit: an int when set, or nil
// (JSON null) when blank.
func (l limit) value() interface{} {
	s := sdk.TrimSpace(string(l))
	if s == "" {
		return nil
	}
	return sdk.Atoi(s)
}

// -----------------------------------------------------------------------------
// Environment building
// -----------------------------------------------------------------------------

// deployEnvironment builds the create-time environment. Only egg variables that
// are explicitly overridden via service_config are set; the panel applies the
// egg's own defaults for the rest.
func deployEnvironment(args sdk.Args, eggVars sdk.Result) []byte {
	env := sdk.NewJSON()
	eggVars.ForEach(func(_, item sdk.Result) bool {
		key := item.Get("attributes.env_variable").String()
		if key == "" {
			return true
		}
		if ov := cfgStr(args, key); ov != "" {
			env.Set(key, ov)
		}
		return true
	})
	return env.Build()
}

// updateEnvironment builds the update-time environment. Each egg variable
// resolves to its service_config override, else the server's current value,
// else the egg default — so a package change never blanks an existing value.
func updateEnvironment(args sdk.Args, eggVars sdk.Result, current sdk.Result) []byte {
	env := sdk.NewJSON()
	eggVars.ForEach(func(_, item sdk.Result) bool {
		attr := item.Get("attributes")
		key := attr.Get("env_variable").String()
		if key == "" {
			return true
		}
		if ov := cfgStr(args, key); ov != "" {
			env.Set(key, ov)
		} else if cur := current.Get(key); cur.Exists() {
			env.Set(key, cur.String())
		} else if def := attr.Get("default_value"); def.Exists() {
			env.Set(key, def.String())
		}
		return true
	})
	return env.Build()
}

// -----------------------------------------------------------------------------
// Additional ports
// -----------------------------------------------------------------------------

// additionalPorts translates the additional_ports config into WISP's
// additional-port specification, so the panel resolves the extra allocations
// server-side. The config is a JSON object keyed by offset-from-primary; each
// value is an egg variable name ("RCON_PORT"), a variable with a range
// ("RCON_PORT:27015-27020" or "RCON_PORT:5000"), or "NONE"/"NONE:range" for a
// reserve-only port with no variable binding. A bare value becomes an offset
// entry keyed on the JSON offset; a ":range" value becomes a range entry.
//
// It returns nil when the config is empty or not a JSON object, in which case
// the caller omits the field entirely.
func additionalPorts(raw string) []byte {
	parsed := sdk.Parse(raw)
	if !parsed.IsObject() {
		return nil
	}

	var entries [][]byte
	parsed.ForEach(func(offset, value sdk.Result) bool {
		param, spec := splitFirst(value.String(), ':')
		param = sdk.TrimSpace(param)

		entry := sdk.NewJSON()
		if param == "" || sdk.EqualFold(param, "NONE") {
			entry.Set("variable", nil)
		} else {
			entry.Set("variable", param)
		}

		if spec = sdk.TrimSpace(spec); spec != "" {
			from, to, ok := boundedRange(spec)
			if !ok {
				return true
			}
			entry.Set("type", "range").Set("from", from).Set("to", to)
		} else {
			entry.Set("type", "offset").Set("offset", sdk.Atoi(offset.String()))
		}
		entries = append(entries, entry.Build())
		return true
	})

	if len(entries) == 0 {
		return nil
	}
	out := make([]byte, 0)
	out = append(out, '[')
	for i, e := range entries {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	out = append(out, ']')
	return out
}

// -----------------------------------------------------------------------------
// Payload / result builders
// -----------------------------------------------------------------------------

type createParams struct {
	name       string
	userID     string
	image      string
	startup    string
	externalID string
	env        []byte
}

// buildCreatePayload assembles the WISP server-creation body, including the
// deploy block. Additional-port allocation, node tags and a server template are
// attached only when configured.
func buildCreatePayload(cfg deployConfig, c createParams) []byte {
	deploy := sdk.NewJSON().
		Set("locations", jsonArray(wispID(cfg.locationID))).
		Set("dedicated_ip", cfg.dedicatedIP).
		Set("port_range", sdk.JSONStringArray(sdk.SplitCSV(cfg.portRange)))
	if tags := sdk.SplitCSV(cfg.tags); len(tags) > 0 {
		deploy.Set("tags", sdk.JSONStringArray(tags))
	}
	if ports := additionalPorts(cfg.additionalRaw); ports != nil {
		deploy.Set("additional_ports", ports)
		deploy.Set("additional_port_fail_mode", failMode(cfg.failMode))
	}

	body := sdk.NewJSON().
		Set("name", c.name).
		Set("user", wispID(c.userID)).
		Set("egg", wispID(cfg.eggID)).
		Set("docker_image", c.image).
		Set("startup", c.startup).
		Set("oom_disabled", cfg.oomDisabled).
		Set("force_outgoing_ip", cfg.forceOutgoingIP).
		Set("limits", cfg.limits()).
		Set("feature_limits", cfg.featureLimits()).
		Set("deploy", deploy.Build()).
		Set("environment", c.env).
		Set("start_on_completion", true).
		Set("external_id", c.externalID)
	if cfg.serverTemplate != "" {
		body.Set("server_template", cfg.serverTemplate)
	}
	return body.Build()
}

// allocationAddress returns the ip and port of the allocation matching allocID
// within an allocations relationship array. The ok result is false when the
// allocation is not present (e.g. the create response did not include it).
func allocationAddress(allocations sdk.Result, allocID string) (string, int, bool) {
	var ip string
	var port int
	var ok bool
	allocations.ForEach(func(_, item sdk.Result) bool {
		attr := item.Get("attributes")
		if attr.Get("id").String() == allocID {
			ip = attr.Get("ip").String()
			if alias := attr.Get("alias").String(); alias != "" {
				ip = alias
			}
			port = attr.Get("port").Int()
			ok = true
			return false
		}
		return true
	})
	return ip, port, ok
}

// -----------------------------------------------------------------------------
// Small helpers
// -----------------------------------------------------------------------------

// wispHeaders returns the standard headers required by the WISP API. The Accept
// header opts into the v2 media type; the version header lets the panel gate
// version-specific behaviour.
func wispHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization":         "Bearer " + apiKey,
		"Accept":                "Application/vnd.wisp.v2+json",
		"Content-Type":          "application/json",
		"X-Wisp-Module-Version": wispModuleVersion,
	}
}

func serviceID(args sdk.Args) string {
	if id := sdk.TrimSpace(args.Get("service_id").String()); id != "" {
		return id
	}
	return args.User().ID
}

func cfgStr(args sdk.Args, key string) string {
	return sdk.TrimSpace(args.ServiceConfig(key))
}

// wispID encodes a resource id as a JSON value: a bare number when the id is
// purely numeric, otherwise a quoted string. WISP v2 uses opaque string ids
// (ULIDs) while older panels use integers, and casting a string id to a number
// would corrupt it.
func wispID(id string) []byte {
	id = sdk.TrimSpace(id)
	if id == "" {
		return []byte("null")
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return sdk.JsonString(id)
		}
	}
	return []byte(id)
}

// jsonArray wraps a single raw JSON element in a one-element array.
func jsonArray(element []byte) []byte {
	out := make([]byte, 0)
	out = append(out, '[')
	out = append(out, element...)
	out = append(out, ']')
	return out
}

// failMode normalises the additional-port failure mode to one of the two values
// the panel accepts, defaulting to "continue".
func failMode(v string) string {
	if v == "stop" {
		return "stop"
	}
	return "continue"
}

// splitFirst splits s on the first occurrence of sep, returning the part before
// and after it. When sep is absent the whole string is the first part.
func splitFirst(s string, sep byte) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// boundedRange parses a "from-to" or single-port spec into inclusive bounds,
// rejecting anything outside 1-65535.
func boundedRange(spec string) (int, int, bool) {
	from, to, ok := sdk.ParsePortRange(spec)
	if !ok || from < 1 || to > 65535 {
		return 0, 0, false
	}
	return from, to, true
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
