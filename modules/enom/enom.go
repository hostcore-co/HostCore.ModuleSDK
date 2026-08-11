// HostCore Domain Module — Enom
//
// A reference HostCore domain module for the Enom registrar, a sibling to the
// Namecheap module. Enom's reseller API is a single endpoint (interface.asp)
// with the command in the query string; requesting responsetype=xml returns an
// <interface-response> envelope whose <ErrCount> reports success. Like every
// module here it imports only the HostCore.ModuleSDK, reading responses with the
// SDK's XML reader.
//
// Enom addresses a domain by its second-level and top-level labels (SLD/TLD),
// so the module splits the incoming full domain name.
//
// # Input contract
//
//	{
//	  "uid":  "resellerid",            // Enom account login id
//	  "pw":   "xxxxxxxx",              // Enom API password/token
//	  "test": false,                   // target the OT&E test environment
//	  ... per-call fields: domain, domains, years, nameservers, contact, ...
//	}
//
// The credentials are the binding configuration (see enom-module.json); the
// per-call fields are supplied by the platform for each operation.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// TestModuleConnection verifies the store's credentials with a login check.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	c := newClient(sdk.GetArgs())
	if _, msg := c.call("CheckLogin", nil); msg != "" {
		return sdk.Fail(msg, nil)
	}
	return sdk.Success(sdk.NewJSON().Set("connected", true).Build())
}

// CheckDomainAvailability checks each requested domain. Enom's Check command
// takes one SLD/TLD, so the module checks the domains individually.
//
//go:wasmexport CheckDomainAvailability
func CheckDomainAvailability() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	names := sdk.StringList(args.Get("domains"))
	if len(names) == 0 {
		return sdk.Success([]byte(`{"results":[]}`))
	}

	var b []byte
	b = append(b, `{"results":[`...)
	for i, name := range names {
		if i > 0 {
			b = append(b, ',')
		}
		sld, tld := splitDomain(name)
		available := false
		reason := ""
		root, msg := c.call("Check", [][2]string{{"SLD", sld}, {"TLD", tld}})
		if msg != "" {
			reason = msg
		} else if root.Get("RRPCode").Text() == "210" {
			available = true
		} else {
			reason = root.Get("RRPText").Text()
		}
		b = append(b, sdk.NewJSON().
			Set("domain", name).
			Set("available", available).
			Set("premium", false).
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

	sld, tld := splitDomain(args.Get("domain").String())
	params := [][2]string{
		{"SLD", sld},
		{"TLD", tld},
		{"NumYears", sdk.Itoa(args.Get("years").Int())},
	}
	if ns := sdk.StringList(args.Get("nameservers")); len(ns) > 0 {
		for i, host := range ns {
			params = append(params, [2]string{"NS" + sdk.Itoa(i+1), host})
		}
	} else {
		params = append(params, [2]string{"UseDNS", "default"})
	}
	params = append(params, contactParams(args)...)

	root, msg := c.call("Purchase", params)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if code := root.Get("RRPCode").Text(); code != "200" {
		return sdk.Fail("Enom did not register the domain (RRP "+code+").", nil)
	}

	// WHOIS privacy (ID Protect) is a separate Enom service; purchase it best
	// effort when requested so a privacy failure doesn't undo the registration.
	if args.Get("privacy").Bool() {
		c.call("PurchaseServices", [][2]string{
			{"Service", "WPPS"},
			{"SLD", sld},
			{"TLD", tld},
			{"NumYears", sdk.Itoa(args.Get("years").Int())},
		})
	}

	return sdk.Success(sdk.NewJSON().
		Set("registered", true).
		Set("expiry_date", c.expiry(sld, tld)).
		Set("external_ref", root.Get("OrderID").Text()).
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

	sld, tld := splitDomain(args.Get("domain").String())
	root, msg := c.call("TP_CreateOrder", [][2]string{
		{"DomainCount", "1"},
		{"OrderType", "Autoverification"},
		{"SLD1", sld},
		{"TLD1", tld},
		{"AuthInfo1", args.Get("epp_code").String()},
		{"UseContacts", "1"},
	})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("initiated", true).
		Set("external_ref", root.Get("TransferOrderID").Text()).
		Set("status", "pending").
		Build())
}

// RenewDomain extends a registration.
//
//go:wasmexport RenewDomain
func RenewDomain() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	sld, tld := splitDomain(args.Get("domain").String())
	root, msg := c.call("Extend", [][2]string{
		{"SLD", sld},
		{"TLD", tld},
		{"NumYears", sdk.Itoa(args.Get("years").Int())},
	})
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if code := root.Get("RRPCode").Text(); code != "200" {
		return sdk.Fail("Enom did not renew the domain (RRP "+code+").", nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("renewed", true).
		Set("expiry_date", c.expiry(sld, tld)).
		Build())
}

// GetDomainInfo reports Enom's authoritative view of the domain. Enom spreads
// this across commands, so it composes GetDomainInfo (status/expiry), GetDNS
// (nameservers) and GetRegLock (lock).
//
//go:wasmexport GetDomainInfo
func GetDomainInfo() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	sld, tld := splitDomain(args.Get("domain").String())
	info, msg := c.call("GetDomainInfo", [][2]string{{"SLD", sld}, {"TLD", tld}})
	if msg != "" {
		// A domain the account doesn't hold is reported as an API error; surface
		// that as a clean "does not exist".
		return sdk.Success(sdk.NewJSON().Set("exists", false).Build())
	}
	status := info.Get("GetDomainInfo.status")

	var nameservers []string
	if dns, dnsMsg := c.call("GetDNS", [][2]string{{"SLD", sld}, {"TLD", tld}}); dnsMsg == "" {
		for _, e := range dns.Children("dns") {
			if h := e.Text(); h != "" {
				nameservers = append(nameservers, h)
			}
		}
	}

	locked := false
	if lk, lkMsg := c.call("GetRegLock", [][2]string{{"SLD", sld}, {"TLD", tld}}); lkMsg == "" {
		locked = lk.Get("reg-lock").Text() == "1"
	}

	return sdk.Success(sdk.NewJSON().
		Set("exists", true).
		Set("status", mapInfoStatus(status.Get("registrationstatus").Text())).
		Set("expiry_date", enomDate(status.Get("expiration").Text())).
		Set("created_date", "").
		Set("nameservers", sdk.JSONStringArray(nameservers)).
		Set("locked", locked).
		Set("privacy", false).
		Build())
}

// UpdateNameservers replaces the delegation set.
//
//go:wasmexport UpdateNameservers
func UpdateNameservers() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	sld, tld := splitDomain(args.Get("domain").String())
	params := [][2]string{{"SLD", sld}, {"TLD", tld}}
	for i, host := range sdk.StringList(args.Get("nameservers")) {
		params = append(params, [2]string{"NS" + sdk.Itoa(i+1), host})
	}

	root, msg := c.call("ModifyNS", params)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if code := root.Get("RRPCode").Text(); code != "" && code != "200" {
		return sdk.Fail("Enom did not update the nameservers (RRP "+code+").", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("updated", true).Build())
}

// GetDomainEPPCode is unsupported: Enom emails the auth code to the registrant
// (via SynchAuthInfo) rather than returning it over the API.
//
//go:wasmexport GetDomainEPPCode
func GetDomainEPPCode() int32 {
	return sdk.Fail("Enom emails the EPP code to the registrant; it is not available via the API.", []byte(`{"code":"unsupported"}`))
}

// SetDomainLock sets the registrar transfer lock.
//
//go:wasmexport SetDomainLock
func SetDomainLock() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	// UnlockRegistrar is inverted: 0 locks, 1 unlocks.
	unlock := "1"
	if args.Get("locked").Bool() {
		unlock = "0"
	}
	sld, tld := splitDomain(args.Get("domain").String())
	if _, msg := c.call("SetRegLock", [][2]string{
		{"SLD", sld},
		{"TLD", tld},
		{"UnlockRegistrar", unlock},
	}); msg != "" {
		return sdk.Fail(msg, nil)
	}
	return sdk.Success(sdk.NewJSON().Set("locked", args.Get("locked").Bool()).Build())
}

// GetDomainContacts confirms the account can read the domain's WHOIS contacts.
//
//go:wasmexport GetDomainContacts
func GetDomainContacts() int32 {
	args := sdk.GetArgs()
	c := newClient(args)

	sld, tld := splitDomain(args.Get("domain").String())
	if _, msg := c.call("GetContacts", [][2]string{{"SLD", sld}, {"TLD", tld}}); msg != "" {
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

	sld, tld := splitDomain(args.Get("domain").String())
	params := append([][2]string{{"SLD", sld}, {"TLD", tld}}, contactParams(args)...)
	if _, msg := c.call("Contacts", params); msg != "" {
		return sdk.Fail(msg, nil)
	}
	return sdk.Success(sdk.NewJSON().Set("updated", true).Build())
}
