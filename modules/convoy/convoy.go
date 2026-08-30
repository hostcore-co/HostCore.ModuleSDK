// HostCore Service Module — Convoy
//
// This is a HostCore service module for the Convoy KVM panel
// (https://convoypanel.com), which wraps Proxmox to sell virtual machines. It
// provisions and manages servers through the Convoy Application API v4, the
// surface the Paymenter Convoy extension drives.
//
// A Convoy server is addressed by the UUID the panel mints at create time.
// DeployService returns it as "server_id" and HostCore replays it on later
// lifecycle calls.
//
// Convoy separates the panel account from the machine. Each customer gets one
// Convoy user, resolved by e-mail and created on first deploy, and every server
// they own is filed under it. The server's own root password is a second,
// unrelated credential set at build time.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "domain":    "convoy.example.com", // Convoy host (no scheme)
//	  "api_key":   "CONVOY_APP_TOKEN",   // Application API token (Bearer)
//	  "remote_id": "d45309ad-…-01a2e54a421e", // server UUID after deploy
//	  "password":  "root-secret",        // server root password (deploy)
//	  "user":      { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

const (
	// bytesPerMiB converts the mebibyte figures a plan is written in to the
	// bytes Convoy stores limits as. The products overflow a 32-bit int, so
	// every byte quantity is carried as an int64.
	bytesPerMiB = 1 << 20

	// maxServerName is the length Convoy accepts for a server's display name.
	maxServerName = 40

	// passwordLength is the length of a generated root or panel password.
	// Convoy requires 8 to 50 characters spanning four character classes.
	passwordLength = 20
)

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that Convoy is reachable and the application
// token authenticates by listing nodes.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	c := newConvoy(sdk.GetArgs())

	resp := c.get("nodes")
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the Convoy application API token.", resp.Body)
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
		{"order":1,"label":"Node","type":"number","key":"node_id","description":"Convoy node id to deploy on.","regex":"^[0-9]+$","required":true},
		{"order":2,"label":"OS Template","type":"string","key":"template_uuid","description":"UUID of the template to install. Take it from the template itself, not its group.","regex":"","required":true},
		{"order":3,"label":"CPU Cores","type":"number","key":"cpu","description":"Virtual cores assigned to the server.","regex":"^[0-9]+$","required":true},
		{"order":4,"label":"Memory (MiB)","type":"number","key":"memory","description":"Memory assigned to the server.","regex":"^[0-9]+$","required":true},
		{"order":5,"label":"Disk (MiB)","type":"number","key":"disk","description":"Disk assigned to the server.","regex":"^[0-9]+$","required":true},
		{"order":6,"label":"Bandwidth (MiB)","type":"number","key":"bandwidth","description":"Monthly bandwidth allowance. Blank or 0 is unmetered.","regex":"^[0-9]+$","required":false},
		{"order":7,"label":"Snapshots","type":"number","key":"snapshots","description":"How many snapshots the customer may keep.","regex":"^[0-9]+$","required":false},
		{"order":8,"label":"Backups","type":"number","key":"backups","description":"How many backups the customer may keep. Blank is unlimited.","regex":"^[0-9]+$","required":false},
		{"order":9,"label":"IPv4 Addresses","type":"number","key":"ipv4","description":"How many IPv4 addresses to draw from the node's pool. Defaults to 1.","regex":"^[0-9]+$","required":false},
		{"order":10,"label":"IPv6 Addresses","type":"number","key":"ipv6","description":"How many IPv6 addresses to draw from the node's pool.","regex":"^[0-9]+$","required":false},
		{"order":11,"label":"Hostname","type":"string","key":"hostname","description":"The server's hostname. Derived from the service id when blank.","regex":"","required":false},
		{"order":12,"label":"Start After Install","type":"bool","key":"start_on_completion","description":"Power the server on once Convoy finishes building it. Defaults to on.","regex":"","required":false}
	]`))
}

// DeployService resolves (or creates) the customer's Convoy user, draws the
// addresses the plan calls for from the node's pool, and builds the server.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	c := newConvoy(args)

	node := cfgStr(args, "node_id")
	template := cfgStr(args, "template_uuid")
	if node == "" || template == "" {
		return sdk.Fail("node_id and template_uuid are required.", nil)
	}
	for _, key := range []string{"cpu", "memory", "disk"} {
		if cfgStr(args, key) == "" {
			return sdk.Fail(key+" is required.", nil)
		}
	}

	userID, msg := c.resolveUser(args.User())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	addresses, msg := c.drawAddresses(node, ipv4Count(args), sdk.Atoi(cfgStr(args, "ipv6")))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	rootPassword := args.Get("password").String()
	if rootPassword == "" {
		rootPassword = sdk.Password(passwordLength)
	}
	hostname := serverHostname(args)

	body := sdk.NewJSON().
		Set("node_id", sdk.Atoi(node)).
		Set("user_id", int(userID)).
		Set("name", serverName(hostname, args.User())).
		Set("hostname", hostname).
		Set("vmid", nil).
		Set("limits", limits(args, addresses)).
		Set("account_password", rootPassword).
		Set("template_uuid", template).
		Set("should_create_server", true).
		Set("start_on_completion", cfgBool(args, "start_on_completion", true))

	resp := c.post("servers", body.Build())
	if !resp.OK {
		return sdk.Fail("Failed to create the server"+withReason(convoyError(resp))+".", resp.Body)
	}

	server := sdk.GetBytes(resp.Body, "data")
	uuid := server.Get("uuid").String()
	if uuid == "" {
		return sdk.Fail("Convoy accepted the build but returned no server UUID.", resp.Body)
	}

	result := sdk.NewJSON().
		Set("server_id", uuid).
		Set("uuid", uuid).
		Set("hostname", hostname).
		Set("user_id", int(userID)).
		Set("credentials", sdk.NewJSON().
			Set("username", "root").
			Set("password", rootPassword).
			Build())
	if addr := primaryAddress(server); addr != "" {
		result.Set("ip", addr)
	}
	return sdk.Success(result.Build())
}

// SuspendService suspends the server bound to this service.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	return settingsAction("suspend", "suspend")
}

// UnsuspendService restores a suspended server.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	return settingsAction("unsuspend", "unsuspend")
}

// TerminateService deletes the server and the virtual machine behind it.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	args := sdk.GetArgs()
	c := newConvoy(args)
	uuid := serverUUID(args)
	if uuid == "" {
		return sdk.Fail("A server UUID is required to address the server.", nil)
	}

	resp := c.delete("servers/" + uuid)
	// A server Convoy no longer holds is already terminated.
	if !resp.OK && resp.Status != 404 {
		return sdk.Fail("Failed to terminate the server"+withReason(convoyError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService applies a changed plan to the server's build. Convoy replaces
// the address list wholesale, so the addresses the server already holds are
// read back and carried through, topped up when the plan raised a count.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	c := newConvoy(args)
	uuid := serverUUID(args)
	if uuid == "" {
		return sdk.Fail("A server UUID is required to address the server.", nil)
	}

	current := c.get("servers/" + uuid)
	if !current.OK {
		return sdk.Fail("Failed to read the server before updating it"+withReason(convoyError(current))+".", current.Body)
	}
	server := sdk.GetBytes(current.Body, "data")
	node := server.Get("node_id").String()

	held4 := addressIDs(server.Get("limits.addresses.ipv4"))
	held6 := addressIDs(server.Get("limits.addresses.ipv6"))
	kept := make([]int64, 0)
	kept = append(kept, held4...)
	kept = append(kept, held6...)

	topUp, msg := c.drawAddresses(node, ipv4Count(args)-len(held4), sdk.Atoi(cfgStr(args, "ipv6"))-len(held6))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	kept = append(kept, topUp...)

	build := sdk.NewJSON().
		Set("address_ids", sdk.JSONInt64Array(kept)).
		Set("cpu", sdk.Atoi(cfgStr(args, "cpu"))).
		Set("memory", mibToBytes(cfgStr(args, "memory"))).
		Set("disk", mibToBytes(cfgStr(args, "disk"))).
		Set("snapshot_limit", sdk.Atoi(cfgStr(args, "snapshots"))).
		Set("backup_limit", optionalCount(cfgStr(args, "backups"))).
		Set("bandwidth_limit", optionalBytes(cfgStr(args, "bandwidth")))

	resp := c.patch("servers/"+uuid+"/settings/build", build.Build())
	if !resp.OK {
		return sdk.Fail("Failed to apply the new build"+withReason(convoyError(resp))+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("server_id", uuid).Build())
}

// ChangePassword resets the customer's Convoy panel password. It does not touch
// the server's root password: Convoy sets that when it builds the machine and
// exposes no endpoint to change it afterwards.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	c := newConvoy(args)

	userID, msg := c.resolveUser(args.User())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	existing := c.get("users/" + sdk.Itoa64(userID))
	if !existing.OK {
		return sdk.Fail("Failed to read the Convoy user"+withReason(convoyError(existing))+".", existing.Body)
	}
	user := sdk.GetBytes(existing.Body, "data")

	password := args.Get("password").String()
	if password == "" {
		password = sdk.Password(passwordLength)
	}

	// Convoy's update endpoint replaces the record, so the fields that are not
	// changing are read back and sent unchanged.
	resp := c.put("users/"+sdk.Itoa64(userID), sdk.NewJSON().
		Set("name", user.Get("name").String()).
		Set("email", user.Get("email").String()).
		Set("root_admin", user.Get("root_admin").Bool()).
		Set("password", password).
		Build())
	if !resp.OK {
		return sdk.Fail("Failed to change the panel password"+withReason(convoyError(resp))+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("password", password).Build())
}

// ServiceAction signs the customer in to the Convoy panel. Convoy keeps power
// control on its client API, which answers to a customer's own token rather
// than the application token this module holds, so the panel is where a
// customer starts and stops their server — and a single sign-on token is how
// they get there without a password.
//
//go:wasmexport ServiceAction
func ServiceAction() int32 {
	args := sdk.GetArgs()

	action := sdk.ToLower(sdk.TrimSpace(args.Get("action").String()))
	if action != "sso" {
		return sdk.Fail("Convoy's application API cannot "+action+" a server; that is done from the Convoy panel.", []byte(`{"code":"unsupported"}`))
	}

	c := newConvoy(args)
	userID, msg := c.resolveUser(args.User())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	resp := c.post("users/"+sdk.Itoa64(userID)+"/generate-sso-token", nil)
	if !resp.OK {
		return sdk.Fail("Failed to create a sign-in link"+withReason(convoyError(resp))+".", resp.Body)
	}
	token := sdk.GetBytes(resp.Body, "data.token").String()
	if token == "" {
		return sdk.Fail("Convoy returned no sign-in token.", resp.Body)
	}

	// The token is single-use and expires in about two minutes, so the URL is
	// built fresh on every click rather than stored anywhere.
	return sdk.Success(sdk.NewJSON().
		Set("url", "https://"+args.Get("domain").String()+"/authenticate?token="+sdk.URLEncode(token)).
		Build())
}

// settingsAction posts one of the suspend/unsuspend settings endpoints.
func settingsAction(endpoint, verb string) int32 {
	args := sdk.GetArgs()
	c := newConvoy(args)
	uuid := serverUUID(args)
	if uuid == "" {
		return sdk.Fail("A server UUID is required to address the server.", nil)
	}

	resp := c.post("servers/"+uuid+"/settings/"+endpoint, nil)
	if !resp.OK {
		return sdk.Fail("Failed to "+verb+" the server"+withReason(convoyError(resp))+".", resp.Body)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// Convoy client
// -----------------------------------------------------------------------------

// convoy is a thin wrapper over the Convoy Application API. It centralises the
// base URL and the bearer authorisation header.
type convoy struct {
	base string
	hdrs map[string]string
}

func newConvoy(args sdk.Args) convoy {
	return convoy{
		base: "https://" + args.Get("domain").String() + "/api/application/",
		hdrs: map[string]string{
			"Authorization": "Bearer " + sdk.TrimSpace(args.Get("api_key").String()),
			"Accept":        "application/json",
			"Content-Type":  "application/json",
		},
	}
}

func (c convoy) get(endpoint string) sdk.HTTPResponse { return sdk.HTTPGet(c.base+endpoint, c.hdrs) }
func (c convoy) delete(endpoint string) sdk.HTTPResponse {
	return sdk.HTTPDelete(c.base+endpoint, c.hdrs)
}

func (c convoy) post(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPost(c.base+endpoint, c.hdrs, body)
}

func (c convoy) put(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPut(c.base+endpoint, c.hdrs, body)
}

func (c convoy) patch(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPatch(c.base+endpoint, c.hdrs, body)
}

// resolveUser finds the customer's Convoy user by e-mail, creating one when
// absent. Convoy's e-mail filter matches loosely, so the returned rows are
// checked for the exact address rather than trusting the first hit.
func (c convoy) resolveUser(u sdk.User) (int64, string) {
	existing := c.get("users?filter%5Bemail%5D=" + sdk.URLEncode(u.Email))
	if !existing.OK {
		return 0, "Failed to look up the Convoy user" + withReason(convoyError(existing)) + "."
	}

	matched := int64(0)
	sdk.GetBytes(existing.Body, "data").ForEach(func(_, item sdk.Result) bool {
		if !sdk.EqualFold(item.Get("email").String(), u.Email) {
			return true
		}
		matched = item.Get("id").Int64()
		return false
	})
	if matched != 0 {
		return matched, ""
	}

	name := sdk.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = "HostCore Client"
	}
	created := c.post("users", sdk.NewJSON().
		Set("name", name).
		Set("email", u.Email).
		Set("password", sdk.Password(passwordLength)).
		Set("root_admin", false).
		Build())
	if !created.OK {
		return 0, "Failed to create the Convoy user" + withReason(convoyError(created)) + "."
	}
	return sdk.GetBytes(created.Body, "data.id").Int64(), ""
}

// drawAddresses takes unallocated addresses of each family from the node's
// pool. A count of zero or less asks for none, which is what a plan that has
// not raised its allowance since the last build wants.
func (c convoy) drawAddresses(node string, ipv4, ipv6 int) ([]int64, string) {
	out := make([]int64, 0)
	for _, family := range [][2]any{{"ipv4", ipv4}, {"ipv6", ipv6}} {
		kind, _ := family[0].(string)
		want, _ := family[1].(int)
		if want <= 0 {
			continue
		}

		resp := c.get("nodes/" + node + "/addresses?filter%5Bserver_id%5D=&filter%5Btype%5D=" + kind + "&per_page=" + sdk.Itoa(want))
		if !resp.OK {
			return nil, "Failed to read the node's " + kind + " pool" + withReason(convoyError(resp)) + "."
		}

		drawn := addressIDs(sdk.GetBytes(resp.Body, "data"))
		if len(drawn) < want {
			return nil, "The node has " + sdk.Itoa(len(drawn)) + " unallocated " + kind + " addresses but the plan needs " + sdk.Itoa(want) + "."
		}
		out = append(out, drawn...)
	}
	return out, ""
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// limits builds the limits object Convoy takes at build time. Memory, disk and
// bandwidth are written in mebibytes by the plan and stored as bytes.
func limits(args sdk.Args, addresses []int64) []byte {
	return sdk.NewJSON().
		Set("cpu", sdk.Atoi(cfgStr(args, "cpu"))).
		Set("memory", mibToBytes(cfgStr(args, "memory"))).
		Set("disk", mibToBytes(cfgStr(args, "disk"))).
		Set("snapshots", sdk.Atoi(cfgStr(args, "snapshots"))).
		Set("backups", optionalCount(cfgStr(args, "backups"))).
		Set("bandwidth", optionalBytes(cfgStr(args, "bandwidth"))).
		Set("address_ids", sdk.JSONInt64Array(addresses)).
		Build()
}

// mibToBytes converts a mebibyte figure to bytes. The result exceeds a 32-bit
// int for any ordinary disk, so it is carried as an int64.
func mibToBytes(mib string) int64 {
	return sdk.Atoi64(mib) * bytesPerMiB
}

// optionalBytes renders a byte limit Convoy treats as unmetered when null.
func optionalBytes(mib string) []byte {
	if b := mibToBytes(mib); b > 0 {
		return []byte(sdk.Itoa64(b))
	}
	return []byte("null")
}

// optionalCount renders a count Convoy treats as unlimited when null.
func optionalCount(value string) []byte {
	if value == "" {
		return []byte("null")
	}
	return []byte(sdk.Itoa(sdk.Atoi(value)))
}

// addressIDs collects the ids out of a list of Convoy address objects.
func addressIDs(list sdk.Result) []int64 {
	out := make([]int64, 0)
	list.ForEach(func(_, item sdk.Result) bool {
		if id := item.Get("id").Int64(); id != 0 {
			out = append(out, id)
		}
		return true
	})
	return out
}

// primaryAddress is the first IPv4 the server was built with, falling back to
// its first IPv6 for an address-family-v6-only plan.
func primaryAddress(server sdk.Result) string {
	for _, family := range []string{"limits.addresses.ipv4", "limits.addresses.ipv6"} {
		found := ""
		server.Get(family).ForEach(func(_, item sdk.Result) bool {
			if addr := item.Get("address").String(); addr != "" {
				found = addr
				return false
			}
			return true
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// ipv4Count is how many IPv4 addresses the plan calls for, defaulting to one:
// a server with no address is unreachable, which is never what a plan means to
// order by leaving the field blank.
func ipv4Count(args sdk.Args) int {
	if v := cfgStr(args, "ipv4"); v != "" {
		return sdk.Atoi(v)
	}
	return 1
}

// serverHostname is the hostname the customer's machine answers to.
func serverHostname(args sdk.Args) string {
	if h := cfgStr(args, "hostname"); h != "" {
		return h
	}
	if id := sdk.TrimSpace(args.Get("service_id").String()); id != "" {
		return "vps" + id
	}
	return "vps" + sdk.RandomString(6)
}

// serverName is the label the server carries in the Convoy panel, tying the
// machine to the customer who owns it within the length Convoy accepts.
func serverName(hostname string, u sdk.User) string {
	name := sdk.TrimSpace(hostname + " " + sdk.TrimSpace(u.FirstName+" "+u.LastName))
	return truncate(name, maxServerName)
}

// truncate shortens s to at most n bytes without splitting a multi-byte rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// convoyError extracts the most useful failure text from a Convoy response:
// the first validation error, then the message field, else the status.
func convoyError(resp sdk.HTTPResponse) string {
	first := ""
	sdk.GetBytes(resp.Body, "errors").ForEach(func(_, v sdk.Result) bool {
		if detail := v.Get("detail").String(); detail != "" {
			first = detail
			return false
		}
		return true
	})
	if first != "" {
		return first
	}
	if m := sdk.GetBytes(resp.Body, "message").String(); m != "" {
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

// cfgBool reads a boolean service_config value, falling back to def when the
// plan leaves the key unset.
func cfgBool(args sdk.Args, key string, def bool) bool {
	v := cfgStr(args, key)
	if v == "" {
		return def
	}
	return sdk.ParseBool(v)
}

// serverUUID reads the server UUID HostCore replays for the service. The
// platform sends it as remote_id; the deploy result names it server_id.
func serverUUID(args sdk.Args) string {
	for _, key := range []string{"remote_id", "remote_uuid", "server_id", "uuid"} {
		if v := sdk.TrimSpace(args.Get(key).String()); v != "" {
			return v
		}
	}
	return ""
}
