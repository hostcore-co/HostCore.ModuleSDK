// HostCore Service Module — Plesk
//
// This is a reference HostCore service module for Plesk (Obsidian) web hosting.
// It provisions and manages hosting subscriptions through the Plesk REST API v2
// (https://docs.plesk.com/en-US/obsidian/api-rpc/about-rest-api.79359/).
//
// The legacy Plesk WHMCS integration drives the XML-RPC packet API; this module
// instead uses the modern JSON REST API and its CLI gateway
// (POST /api/v2/cli/<utility>/call), which runs the same Plesk command-line
// utilities server-side and returns JSON. A CLI call succeeds when its response
// "code" is 0; otherwise "stderr" carries the reason.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "domain":     "plesk.example.com",  // Plesk server host (no scheme)
//	  "api_key":    "PLESK_API_KEY",      // Plesk REST API key (X-API-Key)
//	  "service_id": "1024",               // store service id (used to derive logins)
//	  "password":   "subscription-secret",// subscription system-user password
//	  "user":       { "id", "email", "first_name", "last_name" },
//	  "service_config": [ { "key": "...", "value": "..." }, ... ]
//	}
//
// The subscription domain and service plan come from service_config.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that Plesk is reachable and the API key
// authenticates by requesting server information.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	p := newPlesk(sdk.GetArgs())

	resp := p.get("server")
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the Plesk API key.", resp.Body)
	}
	if !resp.OK {
		return sdk.Fail("Connection failed with status "+sdk.Itoa(resp.Status)+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().
		Set("hostname", sdk.GetBytes(resp.Body, "hostname").String()).
		Build())
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Domain","type":"string","key":"domain","description":"Primary domain of the subscription.","regex":"","required":true},
		{"order":2,"label":"Service Plan","type":"string","key":"plan","description":"Name of the Plesk service plan to provision under.","regex":"","required":true},
		{"order":3,"label":"System User","type":"string","key":"username","description":"System (FTP) login for the subscription. Derived from the domain when blank.","regex":"^[a-z][a-z0-9_]{0,31}$","required":false},
		{"order":4,"label":"IP Address","type":"string","key":"ip","description":"IP address to assign. Plesk auto-selects when blank.","regex":"","required":false},
		{"order":5,"label":"Plesk Port","type":"number","key":"port","description":"Plesk REST API port. Defaults to 8443.","regex":"^[0-9]+$","required":false},
		{"order":6,"label":"Customer Login","type":"string","key":"customer_login","description":"Owning customer login. Derived from the client when blank.","regex":"","required":false}
	]`))
}

// DeployService creates the owning customer (when absent) and the subscription.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	p := newPlesk(args)

	domain := cfgStr(args, "domain")
	plan := cfgStr(args, "plan")
	if domain == "" || plan == "" {
		return sdk.Fail("domain and plan are required.", nil)
	}

	password := args.Get("password").String()
	if password == "" {
		password = sdk.RandomString(16)
	}
	customer := customerLogin(args)
	sysUser := systemUser(args, domain)

	// Create the owning customer if it does not already exist.
	if code, _ := p.cli("customer", "--info", customer); code != 0 {
		u := args.User()
		name := sdk.TrimSpace(u.FirstName + " " + u.LastName)
		if name == "" {
			name = customer
		}
		if code, reason := p.cli("customer", "--create", customer,
			"-passwd", password, "-name", name, "-email", u.Email); code != 0 {
			return sdk.Fail("Failed to create the customer"+withReason(reason)+".", nil)
		}
	}

	params := []string{"--create", domain, "-owner", customer, "-service-plan", plan,
		"-login", sysUser, "-passwd", password}
	if ip := cfgStr(args, "ip"); ip != "" {
		params = append(params, "-ip", ip)
	}
	if code, reason := p.cli("subscription", params...); code != 0 {
		return sdk.Fail("Failed to create the subscription"+withReason(reason)+".", nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("domain", domain).
		Set("customer", customer).
		Set("system_user", sysUser).
		Build())
}

// SuspendService suspends the subscription bound to this service.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	return subscriptionAction("--suspend", "suspend")
}

// UnsuspendService reactivates a suspended subscription.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	return subscriptionAction("--activate", "unsuspend")
}

// TerminateService permanently removes the subscription bound to this service.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	return subscriptionAction("--remove", "terminate")
}

// UpdateService switches the subscription to a different service plan.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	p := newPlesk(args)

	plan := cfgStr(args, "plan")
	if plan == "" {
		return sdk.Fail("plan is required.", nil)
	}
	if code, reason := p.cli("subscription", "--switch-subscription", cfgStr(args, "domain"),
		"-service-plan", plan); code != 0 {
		return sdk.Fail("Failed to switch the service plan"+withReason(reason)+".", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("plan", plan).Build())
}

// ChangePassword updates the subscription's system-user password.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	p := newPlesk(args)

	password := args.Get("password").String()
	if password == "" {
		return sdk.Fail("The password cannot be empty.", nil)
	}
	if code, reason := p.cli("subscription", "--update", cfgStr(args, "domain"),
		"-login", systemUser(args, cfgStr(args, "domain")), "-passwd", password); code != 0 {
		return sdk.Fail("Failed to change the password"+withReason(reason)+".", nil)
	}
	return sdk.Success(nil)
}

// subscriptionAction runs a single-flag subscription CLI command against the
// service's domain.
func subscriptionAction(flag, verb string) int32 {
	args := sdk.GetArgs()
	p := newPlesk(args)

	domain := cfgStr(args, "domain")
	if domain == "" {
		return sdk.Fail("domain is required.", nil)
	}
	if code, reason := p.cli("subscription", flag, domain); code != 0 {
		return sdk.Fail("Failed to "+verb+" the subscription"+withReason(reason)+".", nil)
	}
	return sdk.Success(nil)
}

// -----------------------------------------------------------------------------
// Plesk client
// -----------------------------------------------------------------------------

// plesk is a thin wrapper over the Plesk REST API v2. It centralises the base
// URL and the X-API-Key authorisation header.
type plesk struct {
	base string
	hdrs map[string]string
}

func newPlesk(args sdk.Args) plesk {
	port := cfgStr(args, "port")
	if port == "" {
		port = "8443"
	}
	return plesk{
		base: "https://" + args.Get("domain").String() + ":" + port + "/api/v2/",
		hdrs: map[string]string{
			"X-API-Key":    sdk.TrimSpace(args.Get("api_key").String()),
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
	}
}

func (p plesk) get(endpoint string) sdk.HTTPResponse {
	return sdk.HTTPGet(p.base+endpoint, p.hdrs)
}

// cli runs a Plesk command-line utility through the REST CLI gateway and
// returns its exit code and, on failure, the trimmed stderr as the reason. A
// zero code indicates success.
func (p plesk) cli(utility string, params ...string) (int, string) {
	body := sdk.NewJSON().Set("params", sdk.JSONStringArray(params)).Build()
	resp := sdk.HTTPPost(p.base+"cli/"+utility+"/call", p.hdrs, body)
	if !resp.OK {
		return -1, "status " + sdk.Itoa(resp.Status)
	}
	code := sdk.GetBytes(resp.Body, "code").Int()
	if code == 0 {
		return 0, ""
	}
	reason := sdk.TrimSpace(sdk.GetBytes(resp.Body, "stderr").String())
	if reason == "" {
		reason = sdk.TrimSpace(sdk.GetBytes(resp.Body, "stdout").String())
	}
	return code, reason
}

// ServiceAction signs the customer into Plesk. The panel mints a single-use
// login link for the owning customer, so the store never has to hand out the
// account password.
//
//go:wasmexport ServiceAction
func ServiceAction() int32 {
	args := sdk.GetArgs()
	action := sdk.ToLower(sdk.TrimSpace(args.Get("action").String()))
	if action != "sso" {
		return sdk.Fail("Plesk cannot "+action+" a subscription.", []byte(`{"code":"unsupported"}`))
	}

	p := newPlesk(args)
	link, reason := p.loginLink(customerLogin(args))
	if reason != "" {
		return sdk.Fail("Failed to create a sign-in link"+withReason(reason)+".", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("url", link).Build())
}

// loginLink asks Plesk for a single-use login URL for a customer. The utility
// writes the URL to stdout, which the general cli helper discards on success
// because every other call only cares whether it worked.
func (p plesk) loginLink(customer string) (string, string) {
	body := sdk.NewJSON().Set("params", sdk.JSONStringArray([]string{"--get-login-link", "-user", customer})).Build()

	resp := sdk.HTTPPost(p.base+"cli/admin/call", p.hdrs, body)
	if !resp.OK {
		return "", "status " + sdk.Itoa(resp.Status)
	}
	if code := sdk.GetBytes(resp.Body, "code").Int(); code != 0 {
		reason := sdk.TrimSpace(sdk.GetBytes(resp.Body, "stderr").String())
		if reason == "" {
			reason = "exit " + sdk.Itoa(code)
		}
		return "", reason
	}

	link := sdk.TrimSpace(sdk.GetBytes(resp.Body, "stdout").String())
	if link == "" {
		return "", "Plesk returned no login link"
	}
	return link, ""
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

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

// customerLogin resolves the owning customer login, using the explicit override
// when set and otherwise deriving a stable login from the client's e-mail and
// id.
func customerLogin(args sdk.Args) string {
	if l := cfgStr(args, "customer_login"); l != "" {
		return l
	}
	local := emailLocalPart(args.User().Email)
	if local == "" {
		local = "client"
	}
	return local + "_" + digitsOnly(serviceID(args))
}

// systemUser resolves the subscription's system (FTP) login, using the explicit
// override when set and otherwise deriving it from the domain.
func systemUser(args sdk.Args, domain string) string {
	if u := cfgStr(args, "username"); u != "" {
		return u
	}
	var b []byte
	for i := 0; i < len(domain); i++ {
		c := domain[i]
		if c == '.' {
			break
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			b = append(b, c)
		}
	}
	if len(b) == 0 || !(b[0] >= 'a' && b[0] <= 'z') {
		b = append([]byte{'p'}, b...)
	}
	if len(b) > 16 {
		b = b[:16]
	}
	return string(b)
}

// emailLocalPart returns the sanitised local part of an e-mail address (the
// portion before '@'), keeping only lower-case alphanumerics.
func emailLocalPart(email string) string {
	var b []byte
	for i := 0; i < len(email); i++ {
		c := email[i]
		if c == '@' {
			break
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
	}
	return string(b)
}

func digitsOnly(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b = append(b, s[i])
		}
	}
	if len(b) == 0 {
		return "0"
	}
	return string(b)
}
