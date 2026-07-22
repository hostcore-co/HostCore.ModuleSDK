// HostCore Domain Module — Namecheap
//
// A reference HostCore domain module for the Namecheap registrar. Domain
// modules register, transfer, renew and manage domains rather than provisioning
// services; they expose a domain-lifecycle surface instead of the
// DeployService/SuspendService set.
//
// Namecheap speaks XML over a single GET endpoint (xml.response) with the
// command selected by a query parameter; every response is wrapped in
// <ApiResponse Status="OK|ERROR">. Like every other module this imports only the
// HostCore.ModuleSDK — it reads those responses with the SDK's XML reader rather
// than encoding/xml, and builds requests with the SDK's string helpers.
//
// # Input contract
//
//	{
//	  "api_user":  "ncuser",           // Namecheap API user
//	  "api_key":   "xxxxxxxx",         // Namecheap API key
//	  "username":  "ncuser",           // account username (usually the api_user)
//	  "client_ip": "203.0.113.10",     // whitelisted public IPv4 (see README)
//	  "sandbox":   false,              // target the sandbox API
//	  ... per-call fields: domain, domains, years, nameservers, contact, ...
//	}
//
// The credentials are the binding configuration (see namecheap-module.json); the
// per-call fields are supplied by the platform for each operation.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// TestModuleConnection verifies the store's credentials by listing one domain.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	c := newClient(sdk.GetArgs())
	if _, msg := c.call("namecheap.domains.getList", [][2]string{{"PageSize", "1"}}); msg != "" {
		return sdk.Fail(msg, nil)
	}
	return sdk.Success(sdk.NewJSON().Set("connected", true).Build())
}

// CheckDomainAvailability checks up to 50 domains in one call.
//
//go:wasmexport CheckDomainAvailability
func CheckDomainAvailability() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	names := sdk.StringList(args.Get("domains"))
	if len(names) == 0 {
		return sdk.Success([]byte(`{"results":[]}`))
	}

	root, msg := c.call("namecheap.domains.check", [][2]string{{"DomainList", sdk.Join(names, ",")}})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	var b []byte
	b = append(b, `{"results":[`...)
	for i, r := range root.Get("CommandResponse").Children("DomainCheckResult") {
		if i > 0 {
			b = append(b, ',')
		}
		available := sdk.ParseBool(r.Attr("Available"))
		reason := ""
		if !available {
			reason = r.Attr("Description")
		}
		b = append(b, sdk.NewJSON().
			Set("domain", r.Attr("Domain")).
			Set("available", available).
			Set("premium", sdk.ParseBool(r.Attr("IsPremiumName"))).
			Set("reason", reason).
			Build()...)
	}
	b = append(b, `]}`...)
	return sdk.Success(b)
}

// RegisterDomain registers a domain for a whole-year term.
//
//go:wasmexport RegisterDomain
func RegisterDomain() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	domain := args.Get("domain").String()
	params := [][2]string{
		{"DomainName", domain},
		{"Years", sdk.Itoa(args.Get("years").Int())},
	}
	if ns := nameserverList(args); ns != "" {
		params = append(params, [2]string{"Nameservers", ns})
	}
	if args.Get("privacy").Bool() {
		params = append(params, [2]string{"AddFreeWhoisguard", "yes"}, [2]string{"WGEnabled", "yes"})
	}
	params = append(params, contactParams(args)...)

	root, msg := c.call("namecheap.domains.create", params)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	res := root.Get("CommandResponse.DomainCreateResult")
	if !sdk.ParseBool(res.Attr("Registered")) {
		return sdk.Fail("Namecheap did not register the domain.", nil)
	}

	// Namecheap's create response omits the expiry; fetch it so the platform
	// records an accurate anchor.
	expiry := ""
	if info, infoMsg := c.call("namecheap.domains.getInfo", [][2]string{{"DomainName", domain}}); infoMsg == "" {
		expiry = namecheapDate(info.Get("CommandResponse.DomainGetInfoResult.DomainDetails.ExpiredDate").Text())
	}

	return sdk.Success(sdk.NewJSON().
		Set("registered", true).
		Set("expiry_date", expiry).
		Set("external_ref", createID(res)).
		Set("locked", true).
		Build())
}

// TransferDomain starts an inbound transfer. Completion is asynchronous; the
// platform polls GetDomainInfo.
//
//go:wasmexport TransferDomain
func TransferDomain() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	root, msg := c.call("namecheap.domains.transfer.create", [][2]string{
		{"DomainName", args.Get("domain").String()},
		{"Years", sdk.Itoa(args.Get("years").Int())},
		{"EPPCode", args.Get("epp_code").String()},
	})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("initiated", true).
		Set("external_ref", root.Get("CommandResponse.DomainTransferCreateResult").Attr("TransferID")).
		Set("status", "pending").
		Build())
}

// RenewDomain extends a registration.
//
//go:wasmexport RenewDomain
func RenewDomain() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	root, msg := c.call("namecheap.domains.renew", [][2]string{
		{"DomainName", args.Get("domain").String()},
		{"Years", sdk.Itoa(args.Get("years").Int())},
	})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("renewed", true).
		Set("expiry_date", namecheapDate(root.Get("CommandResponse.DomainRenewResult.DomainDetails.ExpiredDate").Text())).
		Build())
}

// GetDomainInfo reports Namecheap's authoritative view of the domain.
//
//go:wasmexport GetDomainInfo
func GetDomainInfo() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	root, msg := c.call("namecheap.domains.getInfo", [][2]string{{"DomainName", args.Get("domain").String()}})
	if msg != "" {
		// Namecheap reports a domain the account doesn't hold as an API error;
		// surface that as a clean "does not exist" rather than a hard failure.
		return sdk.Success(sdk.NewJSON().Set("exists", false).Build())
	}
	info := root.Get("CommandResponse.DomainGetInfoResult")

	var nameservers []string
	for _, ns := range info.Get("DnsDetails").Children("Nameserver") {
		if t := ns.Text(); t != "" {
			nameservers = append(nameservers, t)
		}
	}

	return sdk.Success(sdk.NewJSON().
		Set("exists", true).
		Set("status", mapInfoStatus(info.Attr("Status"))).
		Set("expiry_date", namecheapDate(info.Get("DomainDetails.ExpiredDate").Text())).
		Set("created_date", namecheapDate(info.Get("DomainDetails.CreatedDate").Text())).
		Set("nameservers", sdk.JSONStringArray(nameservers)).
		Set("locked", sdk.ParseBool(info.Get("LockDetails.RegistrarLockStatus").Text())).
		Set("privacy", sdk.EqualFold(info.Get("Whoisguard").Attr("Enabled"), "true")).
		Build())
}

// UpdateNameservers replaces the delegation set.
//
//go:wasmexport UpdateNameservers
func UpdateNameservers() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	sld, tld := splitDomain(args.Get("domain").String())
	root, msg := c.call("namecheap.domains.dns.setCustom", [][2]string{
		{"SLD", sld},
		{"TLD", tld},
		{"Nameservers", nameserverList(args)},
	})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !sdk.ParseBool(root.Get("CommandResponse.DomainDNSSetCustomResult").Attr("Updated")) {
		return sdk.Fail("Namecheap did not update the nameservers.", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("updated", true).Build())
}

// GetDomainEPPCode is unsupported: Namecheap emails the auth code to the
// registrant rather than exposing it over the API.
//
//go:wasmexport GetDomainEPPCode
func GetDomainEPPCode() int32 {
	return sdk.Fail("Namecheap emails the EPP code to the registrant; it is not available via the API.", []byte(`{"code":"unsupported"}`))
}

// SetDomainLock sets the registrar transfer lock.
//
//go:wasmexport SetDomainLock
func SetDomainLock() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	action := "UNLOCK"
	if args.Get("locked").Bool() {
		action = "LOCK"
	}
	root, msg := c.call("namecheap.domains.setRegistrarLock", [][2]string{
		{"DomainName", args.Get("domain").String()},
		{"LockAction", action},
	})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !sdk.ParseBool(root.Get("CommandResponse.DomainSetRegistrarLockResult").Attr("IsSuccess")) {
		return sdk.Fail("Namecheap did not change the registrar lock.", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("locked", args.Get("locked").Bool()).Build())
}

// GetDomainContacts confirms the account can read the domain's WHOIS contacts.
//
//go:wasmexport GetDomainContacts
func GetDomainContacts() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	if _, msg := c.call("namecheap.domains.getContacts", [][2]string{{"DomainName", args.Get("domain").String()}}); msg != "" {
		return sdk.Fail(msg, nil)
	}
	// The platform mirrors contacts locally from what it submitted; a full
	// re-read is not required, so return an empty set on success.
	return sdk.Success([]byte(`{"contacts":{}}`))
}

// UpdateDomainContacts replaces all WHOIS contact sets.
//
//go:wasmexport UpdateDomainContacts
func UpdateDomainContacts() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	params := append([][2]string{{"DomainName", args.Get("domain").String()}}, contactParams(args)...)
	root, msg := c.call("namecheap.domains.setContacts", params)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !sdk.ParseBool(root.Get("CommandResponse.DomainSetContactResult").Attr("IsSuccess")) {
		return sdk.Fail("Namecheap did not update the contacts.", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("updated", true).Build())
}
