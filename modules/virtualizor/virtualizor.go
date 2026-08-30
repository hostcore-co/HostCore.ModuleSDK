// HostCore Service Module — Virtualizor
//
// This is a reference HostCore service module for the Softaculous Virtualizor
// VPS control panel. It provisions and manages virtual servers through the
// Virtualizor Admin API (https://www.virtualizor.com/docs/admin-api/).
//
// The Admin API is a single endpoint (index.php) selected by an "act" query
// parameter, authenticated with an API key and API password, returning JSON
// when api=json. A VPS is addressed by the integer vpsid the panel allocates at
// create time; HostCore persists the id DeployService returns and replays it as
// "server_id" on later lifecycle calls.
//
// Field and action names follow the Virtualizor Admin API so an operator can
// verify them against the panel's documentation.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "domain":     "vz.example.com",   // Virtualizor host (no scheme)
//	  "api_key":    "VZ_API_KEY",       // Virtualizor Admin API key
//	  "api_pass":   "VZ_API_PASSWORD",  // Virtualizor Admin API password
//	  "server_id":  "913",              // vpsid (lifecycle calls after deploy)
//	  "password":   "root-secret",      // VPS root password (deploy / change-password)
//	  "user":       { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that Virtualizor is reachable and the API
// credentials authenticate by listing nodes.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	v := newVZ(sdk.GetArgs())

	resp := v.call("servers", nil)
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the Virtualizor API key and password.", resp.Body)
	}
	if !resp.OK {
		return sdk.Fail("Connection failed with status "+sdk.Itoa(resp.Status)+".", resp.Body)
	}
	if reason := vzError(resp); reason != "" {
		return sdk.Fail("Authentication failed"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Plan","type":"number","key":"plan_id","description":"Virtualizor plan id (plid) defining the VPS resources.","regex":"^[0-9]+$","required":true},
		{"order":2,"label":"OS Template","type":"number","key":"os_id","description":"OS template id (osid) to install.","regex":"^[0-9]+$","required":true},
		{"order":3,"label":"Virtualization","type":"string","key":"virt","description":"Virtualization type (kvm, openvz, xen, ...). Defaults to kvm.","regex":"^[a-z0-9]+$","required":false},
		{"order":4,"label":"Node","type":"number","key":"node_id","description":"Server/node id (serid) to deploy on. Auto-selected when blank.","regex":"^[0-9]+$","required":false},
		{"order":5,"label":"IP Addresses","type":"number","key":"ips","description":"Number of IPv4 addresses to assign. Defaults to 1.","regex":"^[0-9]+$","required":false},
		{"order":6,"label":"Hostname","type":"string","key":"hostname","description":"VPS hostname. Derived from the domain when blank.","regex":"","required":false},
		{"order":7,"label":"Disk Space (GB)","type":"number","key":"space","description":"Override the plan disk space.","regex":"^[0-9]+$","required":false},
		{"order":8,"label":"Memory (MB)","type":"number","key":"ram","description":"Override the plan memory.","regex":"^[0-9]+$","required":false},
		{"order":9,"label":"Bandwidth (GB)","type":"number","key":"bandwidth","description":"Override the plan bandwidth.","regex":"^[0-9]+$","required":false},
		{"order":10,"label":"CPU Cores","type":"number","key":"cores","description":"Override the plan CPU cores.","regex":"^[0-9]+$","required":false},
		{"order":11,"label":"Virtualizor Port","type":"number","key":"port","description":"Virtualizor Admin API port. Defaults to 4085.","regex":"^[0-9]+$","required":false}
	]`))
}

// DeployService creates a VPS via the addvs action. Virtualizor creates or
// reuses the end-user account from the supplied e-mail.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	v := newVZ(args)

	plan := cfgStr(args, "plan_id")
	os := cfgStr(args, "os_id")
	if plan == "" || os == "" {
		return sdk.Fail("plan_id and os_id are required.", nil)
	}

	rootPass := args.Get("password").String()
	if rootPass == "" {
		rootPass = sdk.RandomString(16)
	}
	virt := cfgStr(args, "virt")
	if virt == "" {
		virt = "kvm"
	}
	ips := cfgStr(args, "ips")
	if ips == "" {
		ips = "1"
	}

	params := [][2]string{
		{"virt", virt},
		{"plid", plan},
		{"osid", os},
		{"hostname", hostname(args)},
		{"rootpass", rootPass},
		{"ips", ips},
		{"user_email", args.User().Email},
		{"user_pass", rootPass},
	}
	if node := cfgStr(args, "node_id"); node != "" {
		params = append(params, [2]string{"serid", node})
	}
	for _, o := range [][2]string{
		{"space", "space"}, {"ram", "ram"}, {"bandwidth", "bandwidth"}, {"cores", "cores"},
	} {
		if val := cfgStr(args, o[0]); val != "" {
			params = append(params, [2]string{o[1], val})
		}
	}

	resp := v.call("addvs", params)
	vpsid := sdk.GetBytes(resp.Body, "vpsid").String()
	if !resp.OK || vpsid == "" {
		return sdk.Fail("Failed to create the VPS"+withReason(vzError(resp))+".", resp.Body)
	}

	return sdk.Success(sdk.NewJSON().
		Set("server_id", vpsid).
		Set("hostname", hostname(args)).
		Set("ip", sdk.GetBytes(resp.Body, "ips.0").String()).
		Build())
}

// SuspendService suspends the VPS bound to this service.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	return vpsAction("suspend", "suspend")
}

// UnsuspendService restores a suspended VPS.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	return vpsAction("unsuspend", "unsuspend")
}

// TerminateService permanently deletes the VPS bound to this service.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	args := sdk.GetArgs()
	v := newVZ(args)
	id := serverID(args)
	if id == "" {
		return sdk.Fail("server_id is required.", nil)
	}
	resp := v.call("vs", [][2]string{{"delete", id}})
	if !resp.OK || vzError(resp) != "" {
		return sdk.Fail("Failed to terminate the VPS"+withReason(vzError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService moves the VPS to a different plan.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	v := newVZ(args)
	id := serverID(args)
	plan := cfgStr(args, "plan_id")
	if id == "" || plan == "" {
		return sdk.Fail("server_id and plan_id are required.", nil)
	}
	resp := v.call("editvs", [][2]string{{"vpsid", id}, {"plid", plan}})
	if !resp.OK || vzError(resp) != "" {
		return sdk.Fail("Failed to change the plan"+withReason(vzError(resp))+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("plan_id", sdk.Atoi(plan)).Build())
}

// ChangePassword sets a new VPS root password.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	v := newVZ(args)
	id := serverID(args)
	password := args.Get("password").String()
	if id == "" {
		return sdk.Fail("server_id is required.", nil)
	}
	if password == "" {
		return sdk.Fail("The password cannot be empty.", nil)
	}
	resp := v.call("editvs", [][2]string{{"vpsid", id}, {"rootpass", password}})
	if !resp.OK || vzError(resp) != "" {
		return sdk.Fail("Failed to change the password"+withReason(vzError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// vpsAction runs a single-target VPS action (suspend/unsuspend) against the
// service's vpsid.
func vpsAction(action, verb string) int32 {
	args := sdk.GetArgs()
	v := newVZ(args)
	id := serverID(args)
	if id == "" {
		return sdk.Fail("server_id is required.", nil)
	}
	resp := v.call("vs", [][2]string{{action, id}})
	if !resp.OK || vzError(resp) != "" {
		return sdk.Fail("Failed to "+verb+" the VPS"+withReason(vzError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// Virtualizor client
// -----------------------------------------------------------------------------

// vz is a thin wrapper over the Virtualizor Admin API. It centralises the base
// URL and the API credentials, which travel in the query string.
type vz struct {
	base string
	auth string
}

func newVZ(args sdk.Args) vz {
	port := cfgStr(args, "port")
	if port == "" {
		port = "4085"
	}
	pass := sdk.TrimSpace(args.Get("api_pass").String())
	if pass == "" {
		pass = cfgStr(args, "api_pass")
	}
	return vz{
		base: "https://" + args.Get("domain").String() + ":" + port + "/index.php",
		auth: "api=json&apikey=" + sdk.URLEncode(sdk.TrimSpace(args.Get("api_key").String())) +
			"&apipass=" + sdk.URLEncode(pass),
	}
}

// call invokes an Admin API action with its parameters in the query string.
func (v vz) call(act string, params [][2]string) sdk.HTTPResponse {
	url := v.base + "?" + v.auth + "&act=" + act
	for _, p := range params {
		url += "&" + sdk.URLEncode(p[0]) + "=" + sdk.URLEncode(p[1])
	}
	return sdk.HTTPGet(url, map[string]string{"Accept": "application/json"})
}

// ServiceAction runs a customer-initiated power change on the VPS. Virtualizor
// takes these on the same Admin API the rest of the module drives.
//
//go:wasmexport ServiceAction
func ServiceAction() int32 {
	args := sdk.GetArgs()
	action := sdk.ToLower(sdk.TrimSpace(args.Get("action").String()))
	if !powerActions[action] {
		return sdk.Fail("Virtualizor cannot "+action+" a VPS.", []byte(`{"code":"unsupported"}`))
	}

	id := serverID(args)
	if id == "" {
		return sdk.Fail("server_id is required.", nil)
	}

	v := newVZ(args)
	resp := v.call("vs", [][2]string{{"action", action}, {"vpsid", id}})
	if !resp.OK {
		return sdk.Fail("Failed to "+action+" the VPS (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	if reason := vzError(resp); reason != "" {
		return sdk.Fail("Failed to "+action+" the VPS"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("action", action).Build())
}

// powerActions are the power changes the Admin API accepts under act=vs.
var powerActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// vzError returns the first message from a Virtualizor "error" field, which may
// be an array or an object keyed by field, or "" when there is no error.
func vzError(resp sdk.HTTPResponse) string {
	e := sdk.GetBytes(resp.Body, "error")
	if !e.Exists() {
		return ""
	}
	var msg string
	e.ForEach(func(_, value sdk.Result) bool {
		msg = value.String()
		return false
	})
	if msg == "" {
		msg = e.String()
	}
	return msg
}

func withReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

func cfgStr(args sdk.Args, key string) string {
	return sdk.TrimSpace(args.ServiceConfig(key))
}

func serverID(args sdk.Args) string {
	return sdk.TrimSpace(args.Get("server_id").String())
}

// hostname resolves the VPS hostname, using the explicit override when set and
// otherwise deriving one from the service's domain or id.
func hostname(args sdk.Args) string {
	if h := cfgStr(args, "hostname"); h != "" {
		return h
	}
	if d := cfgStr(args, "domain"); d != "" {
		return d
	}
	id := sdk.TrimSpace(args.Get("service_id").String())
	if id == "" {
		id = args.User().ID
	}
	return "vps" + id + ".hostcore"
}
