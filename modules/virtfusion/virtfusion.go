// HostCore Service Module — VirtFusion
//
// This is a reference HostCore service module for the VirtFusion VPS/KVM control
// panel. It provisions and manages virtual servers through the VirtFusion API
// v1 (https://docs.virtfusion.com/), the same surface the VirtFusion WHMCS
// module drives.
//
// VirtFusion servers are addressed by an integer server id that the panel
// allocates at create time. HostCore persists the id returned by DeployService
// and passes it back as "server_id" on subsequent lifecycle calls — there is no
// external-id server lookup as with the game panels.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "domain":     "vf.example.com",   // VirtFusion host (no scheme)
//	  "api_key":    "VF_API_TOKEN",     // VirtFusion API token (Bearer)
//	  "server_id":  "412",              // panel server id (lifecycle calls after deploy)
//	  "user":       { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
//
// The store user's id is used as the VirtFusion extRelationId, linking the panel
// user account to the store client.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that VirtFusion is reachable and the API token
// authenticates by listing packages.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	v := newVF(sdk.GetArgs())

	resp := v.get("packages")
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the VirtFusion API token.", resp.Body)
	}
	if !resp.OK {
		return sdk.Fail("Connection failed with status "+sdk.Itoa(resp.Status)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Hypervisor Group","type":"number","key":"hypervisor_id","description":"Hypervisor group id to deploy into.","regex":"^[0-9]+$","required":true},
		{"order":2,"label":"Package","type":"number","key":"package_id","description":"VirtFusion package id defining the server resources.","regex":"^[0-9]+$","required":true},
		{"order":3,"label":"IPv4 Addresses","type":"number","key":"ipv4","description":"Number of IPv4 addresses to assign. Defaults to 1.","regex":"^[0-9]+$","required":false},
		{"order":4,"label":"Storage (GB)","type":"number","key":"storage","description":"Override the package storage.","regex":"^[0-9]+$","required":false},
		{"order":5,"label":"Memory (MB)","type":"number","key":"memory","description":"Override the package memory.","regex":"^[0-9]+$","required":false},
		{"order":6,"label":"Traffic (GB)","type":"number","key":"traffic","description":"Override the package bandwidth.","regex":"^[0-9]+$","required":false},
		{"order":7,"label":"CPU Cores","type":"number","key":"cpu_cores","description":"Override the package CPU cores.","regex":"^[0-9]+$","required":false},
		{"order":8,"label":"Inbound Network Speed","type":"number","key":"network_speed_inbound","description":"Override the inbound network speed.","regex":"^[0-9]+$","required":false},
		{"order":9,"label":"Outbound Network Speed","type":"number","key":"network_speed_outbound","description":"Override the outbound network speed.","regex":"^[0-9]+$","required":false},
		{"order":10,"label":"Network Profile","type":"number","key":"network_profile","description":"Network profile id.","regex":"^[0-9]+$","required":false},
		{"order":11,"label":"Storage Profile","type":"number","key":"storage_profile","description":"Storage profile id.","regex":"^[0-9]+$","required":false}
	]`))
}

// DeployService resolves (or creates) the VirtFusion user for the client, then
// creates a server under the configured package and hypervisor group.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	v := newVF(args)

	hypervisor := cfgStr(args, "hypervisor_id")
	pkg := cfgStr(args, "package_id")
	if hypervisor == "" || pkg == "" {
		return sdk.Fail("hypervisor_id and package_id are required.", nil)
	}

	userID, msg := v.resolveUser(args.User())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	ipv4 := cfgStr(args, "ipv4")
	if ipv4 == "" {
		ipv4 = "1"
	}
	server := sdk.NewJSON().
		Set("packageId", sdk.Atoi(pkg)).
		Set("userId", int(userID)).
		Set("hypervisorId", sdk.Atoi(hypervisor)).
		Set("ipv4", sdk.Atoi(ipv4))
	// Optional resource overrides, mapping the service_config key to its
	// VirtFusion field. A fixed order keeps the payload deterministic.
	for _, o := range [][2]string{
		{"storage", "storage"},
		{"memory", "memory"},
		{"traffic", "traffic"},
		{"cpu_cores", "cpuCores"},
		{"network_speed_inbound", "networkSpeedInbound"},
		{"network_speed_outbound", "networkSpeedOutbound"},
		{"network_profile", "networkProfile"},
		{"storage_profile", "storageProfile"},
	} {
		if val := cfgStr(args, o[0]); val != "" {
			server.Set(o[1], sdk.Atoi(val))
		}
	}

	resp := v.post("servers", server.Build())
	if resp.Status != 201 {
		return sdk.Fail("Failed to create the server"+withReason(vfError(resp))+".", resp.Body)
	}

	return sdk.Success(sdk.NewJSON().
		Set("server_id", int(sdk.GetBytes(resp.Body, "data.id").Int64())).
		Set("user_id", int(userID)).
		Build())
}

// SuspendService suspends the server bound to this service.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	return serverAction("suspend", "suspend")
}

// UnsuspendService restores a suspended server.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	return serverAction("unsuspend", "unsuspend")
}

// TerminateService permanently deletes the server bound to this service.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	args := sdk.GetArgs()
	v := newVF(args)
	id := serverID(args)
	if id == "" {
		return sdk.Fail("server_id is required.", nil)
	}
	resp := v.delete("servers/" + id)
	// 204 deletes; 404 means the server is already gone, which is success for a
	// termination.
	if resp.Status != 204 && resp.Status != 404 {
		return sdk.Fail("Failed to terminate the server"+withReason(vfError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService moves the server to a different package.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	v := newVF(args)
	id := serverID(args)
	pkg := cfgStr(args, "package_id")
	if id == "" || pkg == "" {
		return sdk.Fail("server_id and package_id are required.", nil)
	}
	resp := v.put("servers/" + id + "/package/" + pkg)
	if resp.Status != 200 {
		return sdk.Fail("Failed to change the package"+withReason(vfError(resp))+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("package_id", sdk.Atoi(pkg)).Build())
}

// ChangePassword resets the client's VirtFusion control-panel password. The
// panel generates the new password and returns it; there is no way to set a
// chosen value through this endpoint.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	v := newVF(args)

	resp := v.post("users/"+args.User().ID+"/byExtRelation/resetPassword", nil)
	if resp.Status != 201 && resp.Status != 200 {
		return sdk.Fail("Failed to reset the password"+withReason(vfError(resp))+".", resp.Body)
	}
	password := sdk.GetBytes(resp.Body, "data.password").String()
	if password == "" {
		password = sdk.GetBytes(resp.Body, "password").String()
	}
	return sdk.Success(sdk.NewJSON().Set("password", password).Build())
}

// serverAction performs a suspend/unsuspend POST for the current service.
func serverAction(action, verb string) int32 {
	args := sdk.GetArgs()
	v := newVF(args)
	id := serverID(args)
	if id == "" {
		return sdk.Fail("server_id is required.", nil)
	}
	resp := v.post("servers/"+id+"/"+action, nil)
	if resp.Status != 204 && resp.Status != 404 {
		return sdk.Fail("Failed to "+verb+" the server"+withReason(vfError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// VirtFusion client
// -----------------------------------------------------------------------------

// vf is a thin wrapper over the VirtFusion API v1. It centralises the base URL
// and the Bearer authorisation header.
type vf struct {
	base string
	hdrs map[string]string
}

func newVF(args sdk.Args) vf {
	return vf{
		base: "https://" + args.Get("domain").String() + "/api/v1/",
		hdrs: map[string]string{
			"Authorization": "Bearer " + sdk.TrimSpace(args.Get("api_key").String()),
			"Accept":        "application/json",
			"Content-Type":  "application/json",
		},
	}
}

func (v vf) get(endpoint string) sdk.HTTPResponse    { return sdk.HTTPGet(v.base+endpoint, v.hdrs) }
func (v vf) delete(endpoint string) sdk.HTTPResponse { return sdk.HTTPDelete(v.base+endpoint, v.hdrs) }

func (v vf) post(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPost(v.base+endpoint, v.hdrs, body)
}

func (v vf) put(endpoint string) sdk.HTTPResponse {
	return sdk.HTTPPut(v.base+endpoint, v.hdrs, nil)
}

// resolveUser finds the VirtFusion user linked to the store client by external
// relation, creating one when absent. The user id is returned for server
// creation.
func (v vf) resolveUser(u sdk.User) (int64, string) {
	existing := v.get("users/" + u.ID + "/byExtRelation")
	if existing.Status == 200 {
		return sdk.GetBytes(existing.Body, "data.id").Int64(), ""
	}
	if existing.Status != 404 {
		return 0, "Failed to look up the user (status " + sdk.Itoa(existing.Status) + ")."
	}

	name := sdk.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = "HostCore Client"
	}
	created := v.post("users", sdk.NewJSON().
		Set("name", name).
		Set("email", u.Email).
		Set("extRelationId", u.ID).
		Build())
	if created.Status != 201 {
		return 0, "Failed to create the user" + withReason(vfError(created)) + "."
	}
	return sdk.GetBytes(created.Body, "data.id").Int64(), ""
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// vfError extracts the most useful failure text from a VirtFusion error
// response: the first validation error, then a message field, else the status.
func vfError(resp sdk.HTTPResponse) string {
	if e := sdk.GetBytes(resp.Body, "errors.0").String(); e != "" {
		return e
	}
	if m := sdk.GetBytes(resp.Body, "msg").String(); m != "" {
		return m
	}
	return "status " + sdk.Itoa(resp.Status)
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
