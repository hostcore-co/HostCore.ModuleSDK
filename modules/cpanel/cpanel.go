// HostCore Service Module — cPanel & WHM
//
// This is a reference HostCore service module for cPanel/WHM web hosting. It
// provisions and manages hosting accounts through WHM API 1
// (https://api.docs.cpanel.net/whm/introduction/), the server-side control
// surface a WHMCS-style integration drives.
//
// Unlike the game-panel modules, cPanel accounts are addressed by their cPanel
// username. Requests are simple authenticated GETs to the WHM JSON API on the
// WHM SSL port (2087 by default); parameters travel in the query string and the
// response envelope reports success via metadata.result.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "domain":     "whm.example.com",   // WHM server host (no scheme)
//	  "api_key":    "WHM_API_TOKEN",     // WHM API token
//	  "username":   "root",              // WHM API user the token belongs to (default root)
//	  "service_id": "1024",              // store service id (used to derive a username)
//	  "password":   "account-secret",    // cPanel account password (deploy / change-password)
//	  "user":       { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
//
// The account's own domain and WHM package come from service_config.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that WHM is reachable and the API token
// authenticates by requesting the panel version.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	w := newWHM(sdk.GetArgs())

	resp := w.call("version", nil)
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the WHM API user and token.", resp.Body)
	}
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Connection failed"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().
		Set("version", sdk.GetBytes(resp.Body, "data.version").String()).
		Build())
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Domain","type":"string","key":"domain","description":"Primary domain of the hosting account.","regex":"","required":true},
		{"order":2,"label":"Package","type":"string","key":"package","description":"WHM package (hosting plan) to create the account under.","regex":"","required":true},
		{"order":3,"label":"Username","type":"string","key":"account_username","description":"cPanel account username. Derived from the domain when blank.","regex":"^[a-z][a-z0-9]{0,15}$","required":false},
		{"order":4,"label":"Contact Email","type":"string","key":"contactemail","description":"Account contact email. Defaults to the client's email.","regex":"","required":false},
		{"order":5,"label":"WHM Port","type":"number","key":"port","description":"WHM API SSL port. Defaults to 2087.","regex":"^[0-9]+$","required":false},
		{"order":6,"label":"Dedicated IP","type":"boolean","key":"dedicated_ip","description":"Assign a dedicated IP to the account.","regex":"","required":false}
	]`))
}

// DeployService creates a new cPanel account via WHM createacct.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	w := newWHM(args)

	domain := cfgStr(args, "domain")
	pkg := cfgStr(args, "package")
	if domain == "" || pkg == "" {
		return sdk.Fail("domain and package are required.", nil)
	}

	username := cfgStr(args, "account_username")
	if username == "" {
		username = deriveUsername(domain, serviceID(args))
	}
	password := args.Get("password").String()
	if password == "" {
		password = sdk.RandomString(16)
	}
	contact := cfgStr(args, "contactemail")
	if contact == "" {
		contact = args.User().Email
	}

	params := [][2]string{
		{"username", username},
		{"domain", domain},
		{"password", password},
		{"plan", pkg},
		{"contactemail", contact},
	}
	if sdk.ParseBool(cfgStr(args, "dedicated_ip")) {
		params = append(params, [2]string{"ip", "y"})
	}

	resp := w.call("createacct", params)
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to create the account"+withReason(reason)+".", resp.Body)
	}

	return sdk.Success(sdk.NewJSON().
		Set("username", username).
		Set("domain", domain).
		Set("ip", sdk.GetBytes(resp.Body, "data.ip").String()).
		Set("nameserver", sdk.GetBytes(resp.Body, "data.nameserver").String()).
		Build())
}

// SuspendService suspends the account bound to this service.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	args := sdk.GetArgs()
	w := newWHM(args)
	resp := w.call("suspendacct", [][2]string{
		{"user", accountUser(args)},
		{"reason", "Suspended via HostCore"},
	})
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to suspend the account"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// UnsuspendService restores a suspended account.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	args := sdk.GetArgs()
	w := newWHM(args)
	resp := w.call("unsuspendacct", [][2]string{{"user", accountUser(args)}})
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to unsuspend the account"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// TerminateService permanently removes the account bound to this service.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	args := sdk.GetArgs()
	w := newWHM(args)
	resp := w.call("removeacct", [][2]string{{"user", accountUser(args)}})
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to terminate the account"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService applies a package/plan change via WHM changepackage.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	w := newWHM(args)

	pkg := cfgStr(args, "package")
	if pkg == "" {
		return sdk.Fail("package is required.", nil)
	}
	resp := w.call("changepackage", [][2]string{
		{"user", accountUser(args)},
		{"pkg", pkg},
	})
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to change the package"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("package", pkg).Build())
}

// ChangePassword updates the cPanel account password.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	w := newWHM(args)

	password := args.Get("password").String()
	if password == "" {
		return sdk.Fail("The password cannot be empty.", nil)
	}
	resp := w.call("passwd", [][2]string{
		{"user", accountUser(args)},
		{"password", password},
	})
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to change the password"+withReason(reason)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// WHM client
// -----------------------------------------------------------------------------

// whm is a thin wrapper over the WHM JSON API. It centralises the base URL and
// the WHM token authorisation header.
type whm struct {
	base string
	hdrs map[string]string
}

func newWHM(args sdk.Args) whm {
	port := cfgStr(args, "port")
	if port == "" {
		port = "2087"
	}
	user := sdk.TrimSpace(args.Get("username").String())
	if user == "" {
		user = "root"
	}
	return whm{
		base: "https://" + args.Get("domain").String() + ":" + port + "/json-api/",
		hdrs: map[string]string{
			"Authorization": "whm " + user + ":" + sdk.TrimSpace(args.Get("api_key").String()),
			"Accept":        "application/json",
		},
	}
}

// call invokes a WHM API 1 function with the given parameters as a query string.
// WHM parameters travel in the query string; the request carries no body.
func (w whm) call(function string, params [][2]string) sdk.HTTPResponse {
	url := w.base + function + "?api.version=1"
	for _, p := range params {
		url += "&" + sdk.URLEncode(p[0]) + "=" + sdk.URLEncode(p[1])
	}
	return sdk.HTTPGet(url, w.hdrs)
}

// ServiceAction signs the customer into cPanel. WHM mints a one-time URL for
// the account, so the customer reaches their control panel without the store
// handing out the account password.
//
//go:wasmexport ServiceAction
func ServiceAction() int32 {
	args := sdk.GetArgs()
	action := sdk.ToLower(sdk.TrimSpace(args.Get("action").String()))
	if action != "sso" {
		return sdk.Fail("WHM cannot "+action+" an account.", []byte(`{"code":"unsupported"}`))
	}

	user := accountUser(args)
	if user == "" {
		return sdk.Fail("An account username is required.", nil)
	}

	w := newWHM(args)
	resp := w.call("create_user_session", [][2]string{{"user", user}, {"service", "cpaneld"}})
	if !resp.OK {
		return sdk.Fail("Failed to create a sign-in link (status "+sdk.Itoa(resp.Status)+").", resp.Body)
	}
	if ok, reason := whmResult(resp); !ok {
		return sdk.Fail("Failed to create a sign-in link"+withReason(reason)+".", resp.Body)
	}

	url := sdk.GetBytes(resp.Body, "data.url").String()
	if url == "" {
		return sdk.Fail("WHM returned no sign-in URL.", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("url", url).Build())
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// whmResult reports whether a WHM API 1 response indicates success, returning
// the panel's failure reason when it does not.
func whmResult(resp sdk.HTTPResponse) (bool, string) {
	if !resp.OK {
		return false, "status " + sdk.Itoa(resp.Status)
	}
	if sdk.GetBytes(resp.Body, "metadata.result").Int() == 1 {
		return true, ""
	}
	return false, sdk.GetBytes(resp.Body, "metadata.reason").String()
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

func serviceID(args sdk.Args) string {
	if id := sdk.TrimSpace(args.Get("service_id").String()); id != "" {
		return id
	}
	return args.User().ID
}

// accountUser resolves the cPanel account username for lifecycle calls, using
// the explicit override when set and otherwise deriving it the same way deploy
// does, so both agree.
func accountUser(args sdk.Args) string {
	if u := cfgStr(args, "account_username"); u != "" {
		return u
	}
	return deriveUsername(cfgStr(args, "domain"), serviceID(args))
}

// deriveUsername builds a cPanel-valid username (lower-case, alphanumeric,
// starting with a letter, at most 16 characters) from the account domain,
// appending part of the service id for uniqueness.
func deriveUsername(domain, serviceID string) string {
	var b []byte
	for i := 0; i < len(domain); i++ {
		c := domain[i]
		if c == '.' {
			break
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
	}
	// Must start with a letter.
	if len(b) == 0 || !(b[0] >= 'a' && b[0] <= 'z') {
		b = append([]byte{'h'}, b...)
	}
	if len(b) > 8 {
		b = b[:8]
	}

	suffix := digitsOnly(serviceID)
	if suffix == "" {
		suffix = "0"
	}
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}

	name := string(b) + suffix
	if len(name) > 16 {
		name = name[:16]
	}
	return name
}

func digitsOnly(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b = append(b, s[i])
		}
	}
	return string(b)
}
