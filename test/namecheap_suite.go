// Namecheap domain module scenarios. Namecheap returns XML; the harness escapes
// non-JSON bodies into a JSON string exactly as the engine does, so the mock
// returns raw XML and the module unwraps + parses it with the SDK XML reader.
// The suite asserts on the parsed results, the RFC 3339 date conversion, the
// request parameters sent and error surfacing.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type namecheapMock struct {
	last  url.Values
	force string // when set, every command returns this raw body
}

const ncErrorXML = `<ApiResponse Status="ERROR"><Errors><Error Number="2019166">Domain not found</Error><Error Number="1">try again</Error></Errors><CommandResponse/></ApiResponse>`

func (m *namecheapMock) route(method, rawURL string, _ map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	q := u.Query()
	m.last = q
	if m.force != "" {
		return 200, m.force
	}

	switch q.Get("Command") {
	case "namecheap.domains.getList":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainGetListResult><Domain ID="1" Name="example.com"/></DomainGetListResult></CommandResponse></ApiResponse>`

	case "namecheap.domains.check":
		var items []string
		for _, name := range strings.Split(q.Get("DomainList"), ",") {
			if strings.HasPrefix(name, "taken") {
				items = append(items, fmt.Sprintf(`<DomainCheckResult Domain=%q Available="false" IsPremiumName="false" Description="Domain is already registered"/>`, name))
			} else {
				items = append(items, fmt.Sprintf(`<DomainCheckResult Domain=%q Available="true" IsPremiumName="true"/>`, name))
			}
		}
		return 200, `<ApiResponse Status="OK"><CommandResponse>` + strings.Join(items, "") + `</CommandResponse></ApiResponse>`

	case "namecheap.domains.create":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainCreateResult Domain="acme.com" Registered="true" DomainID="9911"/></CommandResponse></ApiResponse>`

	case "namecheap.domains.getInfo":
		if q.Get("DomainName") == "missing.com" {
			return 200, ncErrorXML
		}
		return 200, `<ApiResponse Status="OK"><CommandResponse>
			<DomainGetInfoResult Status="Ok" DomainName="acme.com">
				<DomainDetails><CreatedDate>07/19/2024</CreatedDate><ExpiredDate>07/19/2027</ExpiredDate></DomainDetails>
				<LockDetails><RegistrarLockStatus>true</RegistrarLockStatus></LockDetails>
				<Whoisguard Enabled="True"/>
				<DnsDetails><Nameserver>dns1.registrar-servers.com</Nameserver><Nameserver>dns2.registrar-servers.com</Nameserver></DnsDetails>
			</DomainGetInfoResult>
		</CommandResponse></ApiResponse>`

	case "namecheap.domains.transfer.create":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainTransferCreateResult TransferID="55021" Statuscode="11" Domain="acme.com"/></CommandResponse></ApiResponse>`

	case "namecheap.domains.renew":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainRenewResult DomainID="9911" Renew="true"><DomainDetails><ExpiredDate>07/19/2029</ExpiredDate></DomainDetails></DomainRenewResult></CommandResponse></ApiResponse>`

	case "namecheap.domains.dns.setCustom":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainDNSSetCustomResult Domain="acme.com" Updated="true"/></CommandResponse></ApiResponse>`

	case "namecheap.domains.setRegistrarLock":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainSetRegistrarLockResult Domain="acme.com" IsSuccess="true"/></CommandResponse></ApiResponse>`

	case "namecheap.domains.getContacts":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainContactsResult Domain="acme.com"/></CommandResponse></ApiResponse>`

	case "namecheap.domains.setContacts":
		return 200, `<ApiResponse Status="OK"><CommandResponse><DomainSetContactResult Domain="acme.com" IsSuccess="true"/></CommandResponse></ApiResponse>`
	}
	return 200, ncErrorXML
}

func runNamecheap(wasm []byte) {
	namecheapConnection(wasm)
	namecheapCheck(wasm)
	namecheapRegister(wasm)
	namecheapGetInfo(wasm)
	namecheapLifecycle(wasm)
	namecheapErrors(wasm)
}

func ncArgs(extra string) string {
	base := `"api_user":"ncuser","api_key":"nckey","username":"ncuser","client_ip":"203.0.113.10","sandbox":true`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

func namecheapConnection(wasm []byte) {
	fmt.Println("── TestModuleConnection sends the five global params")
	m := &namecheapMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "TestModuleConnection", ncArgs(""))
	assert("connection succeeds", res["success"] == true, res)
	assert("ClientIp sent as request param", m.last.Get("ClientIp") == "203.0.113.10", m.last.Get("ClientIp"))
	assert("ApiUser sent", m.last.Get("ApiUser") == "ncuser", m.last.Get("ApiUser"))
	assert("Command sent", m.last.Get("Command") == "namecheap.domains.getList", m.last.Get("Command"))
}

func namecheapCheck(wasm []byte) {
	fmt.Println("── CheckDomainAvailability parses per-domain results")
	m := &namecheapMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "CheckDomainAvailability", ncArgs(`"domains":["free.io","taken.com","free.io"]`))
	assert("check succeeds", res["success"] == true, res)
	assert("de-duplicated DomainList sent", m.last.Get("DomainList") == "free.io,taken.com", m.last.Get("DomainList"))
	data, _ := res["data"].(map[string]any)
	results, _ := data["results"].([]any)
	assert("two results", len(results) == 2, results)
	r0, _ := results[0].(map[string]any)
	assert("free.io available and premium", r0["available"] == true && r0["premium"] == true, r0)
	r1, _ := results[1].(map[string]any)
	assert("taken.com unavailable with reason", r1["available"] == false && str(r1["reason"]) == "Domain is already registered", r1)
}

func namecheapRegister(wasm []byte) {
	fmt.Println("── RegisterDomain sends contacts/privacy and returns the fetched expiry")
	m := &namecheapMock{}
	plugin := loadPlugin(wasm, m.route)
	args := ncArgs(`"domain":"acme.com","years":2,"privacy":true,"nameservers":["ns1.x.net","ns2.x.net"],` +
		`"contact":{"first_name":"Ada","last_name":"Lovelace","address1":"1 St","city":"London","state":"LDN","postal_code":"E1","country_code":"GB","phone":"+44.2000000000","email":"ada@x.test"}`)
	res := call(plugin, "RegisterDomain", args)
	assert("register succeeds", res["success"] == true, res)

	data, _ := res["data"].(map[string]any)
	assert("registered true", data["registered"] == true, data)
	assert("external_ref from DomainID", data["external_ref"] == "9911", data["external_ref"])
	assert("expiry fetched and converted to RFC3339", data["expiry_date"] == "2027-07-19T00:00:00Z", data["expiry_date"])
	assert("locked true after register", data["locked"] == true, data)

	// A create-only probe to assert the create request carried the right params.
	probe := &namecheapMock{}
	pp := loadPlugin(wasm, func(method, rawURL string, b map[string]any) (int, string) {
		u, _ := url.Parse(rawURL)
		if u.Query().Get("Command") == "namecheap.domains.create" {
			probe.last = u.Query()
			return 200, `<ApiResponse Status="ERROR"><Errors><Error Number="1">stop here</Error></Errors></ApiResponse>`
		}
		return 200, ncErrorXML
	})
	call(pp, "RegisterDomain", args)
	assert("create sent Years", probe.last.Get("Years") == "2", probe.last.Get("Years"))
	assert("create sent whoisguard", probe.last.Get("AddFreeWhoisguard") == "yes" && probe.last.Get("WGEnabled") == "yes", probe.last)
	assert("create sent Nameservers", probe.last.Get("Nameservers") == "ns1.x.net,ns2.x.net", probe.last.Get("Nameservers"))
	assert("create fanned contact to Registrant + AuxBilling", probe.last.Get("RegistrantFirstName") == "Ada" && probe.last.Get("AuxBillingEmailAddress") == "ada@x.test", probe.last)
}

func namecheapGetInfo(wasm []byte) {
	fmt.Println("── GetDomainInfo maps status, dates, nameservers, lock and privacy")
	m := &namecheapMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "GetDomainInfo", ncArgs(`"domain":"acme.com"`))
	data, _ := res["data"].(map[string]any)
	assert("exists true", data["exists"] == true, data)
	assert("status mapped to active", data["status"] == "active", data["status"])
	assert("expiry converted", data["expiry_date"] == "2027-07-19T00:00:00Z", data["expiry_date"])
	assert("created converted", data["created_date"] == "2024-07-19T00:00:00Z", data["created_date"])
	assert("locked parsed from element text", data["locked"] == true, data["locked"])
	assert("privacy parsed from Whoisguard attr", data["privacy"] == true, data["privacy"])
	ns, _ := data["nameservers"].([]any)
	assert("two nameservers", len(ns) == 2 && ns[0] == "dns1.registrar-servers.com", ns)

	// A domain the account doesn't hold → clean exists:false.
	res2 := call(plugin, "GetDomainInfo", ncArgs(`"domain":"missing.com"`))
	d2, _ := res2["data"].(map[string]any)
	assert("missing domain returns exists:false", res2["success"] == true && d2["exists"] == false, res2)
}

func namecheapLifecycle(wasm []byte) {
	fmt.Println("── transfer / renew / nameservers / lock / contacts / epp")
	m := &namecheapMock{}
	plugin := loadPlugin(wasm, m.route)

	tr := call(plugin, "TransferDomain", ncArgs(`"domain":"acme.com","years":1,"epp_code":"SECRET"`))
	trd, _ := tr["data"].(map[string]any)
	assert("transfer initiated", tr["success"] == true && trd["external_ref"] == "55021" && trd["status"] == "pending", tr)
	assert("EPPCode forwarded", m.last.Get("EPPCode") == "SECRET", m.last.Get("EPPCode"))

	rn := call(plugin, "RenewDomain", ncArgs(`"domain":"acme.com","years":1`))
	rnd, _ := rn["data"].(map[string]any)
	assert("renew returns converted expiry", rn["success"] == true && rnd["expiry_date"] == "2029-07-19T00:00:00Z", rn)

	un := call(plugin, "UpdateNameservers", ncArgs(`"domain":"acme.co.uk","nameservers":["a.ns.net","b.ns.net"]`))
	assert("nameservers updated", un["success"] == true, un)
	assert("domain split into SLD/TLD", m.last.Get("SLD") == "acme" && m.last.Get("TLD") == "co.uk", m.last)

	lk := call(plugin, "SetDomainLock", ncArgs(`"domain":"acme.com","locked":true`))
	assert("lock set", lk["success"] == true, lk)
	assert("LockAction LOCK sent", m.last.Get("LockAction") == "LOCK", m.last.Get("LockAction"))

	assert("get contacts ok", call(plugin, "GetDomainContacts", ncArgs(`"domain":"acme.com"`))["success"] == true, nil)
	uc := call(plugin, "UpdateDomainContacts", ncArgs(`"domain":"acme.com","contact":{"first_name":"Ada","email":"ada@x.test"}`))
	assert("update contacts ok", uc["success"] == true, uc)

	epp := call(plugin, "GetDomainEPPCode", ncArgs(`"domain":"acme.com"`))
	assert("epp code reported unsupported", epp["success"] == false, epp)
}

func namecheapErrors(wasm []byte) {
	fmt.Println("── API ERROR responses surface joined error text")
	m := &namecheapMock{force: ncErrorXML}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "RegisterDomain", ncArgs(`"domain":"acme.com","years":1`))
	assert("register fails on API error", res["success"] == false, res)
	assert("error text joined from Errors", strings.Contains(str(res["error"]), "Domain not found; try again"), res["error"])
}
