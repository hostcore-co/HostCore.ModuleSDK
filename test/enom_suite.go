// Enom domain module scenarios. Enom returns XML under <interface-response>;
// the harness escapes non-JSON bodies exactly as the engine does, so the mock
// returns raw XML and the module unwraps + parses it with the SDK XML reader.
// The suite asserts on parsed results, RRPCode handling, the date conversion,
// request parameters and ErrCount-based error surfacing.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type enomMock struct {
	last  url.Values
	host  string
	force string
}

const enomErrXML = `<interface-response><ErrCount>1</ErrCount><errors><Err1>uid or pw is invalid</Err1></errors><Done>true</Done></interface-response>`

func envelope(inner string) string {
	return `<interface-response>` + inner + `<ErrCount>0</ErrCount><Done>true</Done></interface-response>`
}

func (m *enomMock) route(method, rawURL string, _ map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	q := u.Query()
	m.last = q
	m.host = u.Host
	if m.force != "" {
		return 200, m.force
	}

	switch q.Get("command") {
	case "CheckLogin":
		return 200, envelope(`<CheckLogin>Login successful</CheckLogin>`)
	case "Check":
		if strings.HasPrefix(q.Get("SLD"), "taken") {
			return 200, envelope(`<RRPCode>211</RRPCode><RRPText>Domain not available</RRPText>`)
		}
		return 200, envelope(`<RRPCode>210</RRPCode><RRPText>Domain available</RRPText>`)
	case "Purchase":
		return 200, envelope(`<RRPCode>200</RRPCode><OrderID>777001</OrderID>`)
	case "PurchaseServices":
		return 200, envelope(`<ProductType>WPPS</ProductType>`)
	case "GetDomainExp":
		return 200, envelope(`<GetDomainExp><DomainName>acme.com</DomainName><ExpirationDate>7/19/2027 11:59:00 PM</ExpirationDate></GetDomainExp>`)
	case "TP_CreateOrder":
		return 200, envelope(`<TransferOrderID>55012</TransferOrderID>`)
	case "Extend":
		return 200, envelope(`<RRPCode>200</RRPCode><OrderID>888002</OrderID>`)
	case "GetDomainInfo":
		if q.Get("SLD") == "missing" {
			return 200, enomErrXML
		}
		return 200, envelope(`<GetDomainInfo><domainname>ACME.COM</domainname><status><registrar>eNom</registrar><registrationstatus>Registered</registrationstatus><expiration>7/19/2027 11:59:00 PM</expiration></status></GetDomainInfo>`)
	case "GetDNS":
		return 200, envelope(`<dns>dns1.name-services.com</dns><dns>dns2.name-services.com</dns><Count>2</Count>`)
	case "GetRegLock":
		return 200, envelope(`<reg-lock>1</reg-lock>`)
	case "ModifyNS":
		return 200, envelope(`<RRPCode>200</RRPCode>`)
	case "SetRegLock":
		return 200, envelope(`<RegistrarLock>Locked</RegistrarLock>`)
	case "GetContacts":
		return 200, envelope(`<GetContacts><Registrant><RegistrantFirstName>Ada</RegistrantFirstName></Registrant></GetContacts>`)
	case "Contacts":
		return 200, envelope(``)
	}
	return 200, enomErrXML
}

func runEnom(wasm []byte) {
	enomConnection(wasm)
	enomCheck(wasm)
	enomRegister(wasm)
	enomGetInfo(wasm)
	enomLifecycle(wasm)
	enomErrors(wasm)
}

func enomArgs(extra string) string {
	base := `"uid":"reseller","pw":"secret","test":true`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

func enomConnection(wasm []byte) {
	fmt.Println("── TestModuleConnection performs a login check")
	m := &enomMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "TestModuleConnection", enomArgs(""))
	assert("connection succeeds", res["success"] == true, res)
	assert("uid/pw and xml response requested", m.last.Get("uid") == "reseller" && m.last.Get("responsetype") == "xml", m.last)
	assert("CheckLogin command sent", m.last.Get("command") == "CheckLogin", m.last.Get("command"))
	assert("targets the test (OT&E) endpoint", m.host == "resellertest.enom.com", m.host)
}

func enomCheck(wasm []byte) {
	fmt.Println("── CheckDomainAvailability reads RRPCode per domain")
	m := &enomMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "CheckDomainAvailability", enomArgs(`"domains":["free.io","taken.com"]`))
	assert("check succeeds", res["success"] == true, res)
	data, _ := res["data"].(map[string]any)
	results, _ := data["results"].([]any)
	assert("two results", len(results) == 2, results)
	r0, _ := results[0].(map[string]any)
	assert("free.io available (RRP 210)", r0["available"] == true && r0["domain"] == "free.io", r0)
	r1, _ := results[1].(map[string]any)
	assert("taken.com unavailable with reason", r1["available"] == false && str(r1["reason"]) == "Domain not available", r1)
	// The last Check sent the SLD/TLD split of the second domain.
	assert("check split domain into SLD/TLD", m.last.Get("SLD") == "taken" && m.last.Get("TLD") == "com", m.last)
}

func enomRegister(wasm []byte) {
	fmt.Println("── RegisterDomain sends SLD/TLD, years, nameservers, contacts")
	probe := &enomMock{}
	plugin := loadPlugin(wasm, probe.route)
	args := enomArgs(`"domain":"acme.com","years":2,"privacy":true,"nameservers":["ns1.x.net","ns2.x.net"],` +
		`"contact":{"first_name":"Ada","last_name":"Lovelace","address1":"1 St","city":"London","state":"LDN","postal_code":"E1","country_code":"GB","phone":"+44.2000000000","email":"ada@x.test"}`)
	res := call(plugin, "RegisterDomain", args)
	assert("register succeeds", res["success"] == true, res)
	data, _ := res["data"].(map[string]any)
	assert("registered true", data["registered"] == true, data)
	assert("external_ref from OrderID", data["external_ref"] == "777001", data["external_ref"])
	assert("expiry fetched and converted to RFC3339", data["expiry_date"] == "2027-07-19T00:00:00Z", data["expiry_date"])
	assert("locked true after register", data["locked"] == true, data)

	// Verify the create request via a probe that stops at Purchase.
	created := &enomMock{}
	pp := loadPlugin(wasm, func(method, rawURL string, b map[string]any) (int, string) {
		u, _ := url.Parse(rawURL)
		if u.Query().Get("command") == "Purchase" {
			created.last = u.Query()
			return 200, `<interface-response><ErrCount>1</ErrCount><errors><Err1>stop</Err1></errors></interface-response>`
		}
		return 200, enomErrXML
	})
	call(pp, "RegisterDomain", args)
	assert("purchase sent SLD/TLD", created.last.Get("SLD") == "acme" && created.last.Get("TLD") == "com", created.last)
	assert("purchase sent NumYears", created.last.Get("NumYears") == "2", created.last.Get("NumYears"))
	assert("purchase sent NS1/NS2", created.last.Get("NS1") == "ns1.x.net" && created.last.Get("NS2") == "ns2.x.net", created.last)
	assert("purchase fanned contact to all roles", created.last.Get("RegistrantFirstName") == "Ada" && created.last.Get("AuxBillingEmailAddress") == "ada@x.test", created.last)
}

func enomGetInfo(wasm []byte) {
	fmt.Println("── GetDomainInfo composes status, expiry, nameservers, lock")
	m := &enomMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "GetDomainInfo", enomArgs(`"domain":"acme.com"`))
	data, _ := res["data"].(map[string]any)
	assert("exists true", data["exists"] == true, data)
	assert("status mapped to active", data["status"] == "active", data["status"])
	assert("expiry converted (time dropped)", data["expiry_date"] == "2027-07-19T00:00:00Z", data["expiry_date"])
	assert("locked from GetRegLock", data["locked"] == true, data["locked"])
	ns, _ := data["nameservers"].([]any)
	assert("two nameservers from GetDNS", len(ns) == 2 && ns[0] == "dns1.name-services.com", ns)

	res2 := call(plugin, "GetDomainInfo", enomArgs(`"domain":"missing.com"`))
	d2, _ := res2["data"].(map[string]any)
	assert("missing domain returns exists:false", res2["success"] == true && d2["exists"] == false, res2)
}

func enomLifecycle(wasm []byte) {
	fmt.Println("── transfer / renew / nameservers / lock / contacts / epp")
	m := &enomMock{}
	plugin := loadPlugin(wasm, m.route)

	tr := call(plugin, "TransferDomain", enomArgs(`"domain":"acme.com","years":1,"epp_code":"SECRET"`))
	trd, _ := tr["data"].(map[string]any)
	assert("transfer initiated", tr["success"] == true && trd["external_ref"] == "55012" && trd["status"] == "pending", tr)
	assert("AuthInfo1 (EPP) forwarded", m.last.Get("AuthInfo1") == "SECRET", m.last.Get("AuthInfo1"))

	rn := call(plugin, "RenewDomain", enomArgs(`"domain":"acme.com","years":1`))
	rnd, _ := rn["data"].(map[string]any)
	assert("renew converts expiry", rn["success"] == true && rnd["expiry_date"] == "2027-07-19T00:00:00Z", rn)

	un := call(plugin, "UpdateNameservers", enomArgs(`"domain":"acme.co.uk","nameservers":["a.ns.net","b.ns.net"]`))
	assert("nameservers updated", un["success"] == true, un)
	assert("ModifyNS split SLD/TLD and sent NS1", m.last.Get("SLD") == "acme" && m.last.Get("TLD") == "co.uk" && m.last.Get("NS1") == "a.ns.net", m.last)

	lk := call(plugin, "SetDomainLock", enomArgs(`"domain":"acme.com","locked":true`))
	assert("lock set", lk["success"] == true, lk)
	assert("lock sends UnlockRegistrar=0", m.last.Get("UnlockRegistrar") == "0", m.last.Get("UnlockRegistrar"))
	call(plugin, "SetDomainLock", enomArgs(`"domain":"acme.com","locked":false`))
	assert("unlock sends UnlockRegistrar=1", m.last.Get("UnlockRegistrar") == "1", m.last.Get("UnlockRegistrar"))

	assert("get contacts ok", call(plugin, "GetDomainContacts", enomArgs(`"domain":"acme.com"`))["success"] == true, nil)
	assert("update contacts ok", call(plugin, "UpdateDomainContacts", enomArgs(`"domain":"acme.com","contact":{"first_name":"Ada","email":"ada@x.test"}`))["success"] == true, nil)

	epp := call(plugin, "GetDomainEPPCode", enomArgs(`"domain":"acme.com"`))
	assert("epp code reported unsupported", epp["success"] == false, epp)
}

func enomErrors(wasm []byte) {
	fmt.Println("── ErrCount > 0 surfaces the joined error text")
	m := &enomMock{force: enomErrXML}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "RegisterDomain", enomArgs(`"domain":"acme.com","years":1`))
	assert("register fails on API error", res["success"] == false, res)
	assert("error text from Err1", strings.Contains(str(res["error"]), "uid or pw is invalid"), res["error"])
}
