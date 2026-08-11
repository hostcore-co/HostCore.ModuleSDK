// Namecheap API client for the domain module. Namecheap speaks XML over a
// single GET endpoint (xml.response) with the command in the query string;
// every response is wrapped in <ApiResponse Status="OK|ERROR">. This file owns
// request building, the shared response envelope and the value helpers.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

const (
	liveBase    = "https://api.namecheap.com/xml.response"
	sandboxBase = "https://api.sandbox.namecheap.com/xml.response"
)

// namecheap holds the store's Namecheap credentials, resolved once per call
// from the binding configuration injected into the args.
type namecheap struct {
	apiUser  string
	apiKey   string
	username string
	clientIP string
	base     string
}

func newClient(args sdk.Args) namecheap {
	base := liveBase
	if sdk.ParseBool(args.Get("sandbox").String()) {
		base = sandboxBase
	}
	return namecheap{
		apiUser:  sdk.TrimSpace(args.Get("api_user").String()),
		apiKey:   sdk.TrimSpace(args.Get("api_key").String()),
		username: sdk.TrimSpace(args.Get("username").String()),
		clientIP: sdk.TrimSpace(args.Get("client_ip").String()),
		base:     base,
	}
}

// call issues a Namecheap command and returns the parsed <ApiResponse> root.
// The message is non-empty when the transport failed or the API reported ERROR,
// so callers can fail the module cleanly. The five global parameters
// (ApiUser, ApiKey, UserName, ClientIp, Command) are required on every request.
func (c namecheap) call(command string, params [][2]string) (sdk.XMLNode, string) {
	url := c.base +
		"?ApiUser=" + sdk.URLEncode(c.apiUser) +
		"&ApiKey=" + sdk.URLEncode(c.apiKey) +
		"&UserName=" + sdk.URLEncode(c.username) +
		"&ClientIp=" + sdk.URLEncode(c.clientIP) +
		"&Command=" + sdk.URLEncode(command)
	for _, p := range params {
		url += "&" + sdk.URLEncode(p[0]) + "=" + sdk.URLEncode(p[1])
	}

	resp := sdk.HTTPGet(url, map[string]string{"Accept": "application/xml"})
	if !resp.OK {
		return sdk.XMLNode{}, "Namecheap request failed with status " + sdk.Itoa(resp.Status) + "."
	}

	// The engine escapes non-JSON bodies into a JSON string; unwrap it back to
	// the raw XML before parsing.
	root := sdk.ParseXML([]byte(sdk.ParseBytes(resp.Body).String()))
	if !sdk.EqualFold(root.Attr("Status"), "OK") {
		return root, namecheapError(root)
	}
	return root, ""
}

// namecheapError joins any API-level error texts into one message.
func namecheapError(root sdk.XMLNode) string {
	var parts []string
	for _, e := range root.Get("Errors").Children("Error") {
		if t := e.Text(); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "Namecheap returned an error."
	}
	return sdk.Join(parts, "; ")
}

// createID reads the registered domain's id from a create result, tolerating
// either the DomainID or ID attribute Namecheap has used across versions.
func createID(res sdk.XMLNode) string {
	if id := res.Attr("DomainID"); id != "" {
		return id
	}
	return res.Attr("ID")
}

// nameserverList joins the args' nameserver array into Namecheap's
// comma-separated form.
func nameserverList(args sdk.Args) string {
	return sdk.Join(sdk.StringList(args.Get("nameservers")), ",")
}

// contactParams fans one submitted contact set out to the Registrant, Tech,
// Admin and AuxBilling parameter groups Namecheap requires for
// register/transfer/setContacts. The platform collects a single contact and
// applies it to all four roles.
func contactParams(args sdk.Args) [][2]string {
	c := args.Get("contacts.registrant")
	if !c.Exists() {
		c = args.Get("contact")
	}

	first := c.Get("first_name").String()
	last := c.Get("last_name").String()
	org := c.Get("organization").String()
	addr1 := c.Get("address1").String()
	addr2 := c.Get("address2").String()
	city := c.Get("city").String()
	state := c.Get("state").String()
	postal := c.Get("postal_code").String()
	country := c.Get("country_code").String()
	phone := c.Get("phone").String()
	email := c.Get("email").String()

	var out [][2]string
	for _, role := range []string{"Registrant", "Tech", "Admin", "AuxBilling"} {
		out = append(out,
			[2]string{role + "FirstName", first},
			[2]string{role + "LastName", last},
			[2]string{role + "Address1", addr1},
			[2]string{role + "City", city},
			[2]string{role + "StateProvince", state},
			[2]string{role + "PostalCode", postal},
			[2]string{role + "Country", country},
			[2]string{role + "Phone", phone},
			[2]string{role + "EmailAddress", email},
		)
		if org != "" {
			out = append(out, [2]string{role + "OrganizationName", org})
		}
		if addr2 != "" {
			out = append(out, [2]string{role + "Address2", addr2})
		}
	}
	return out
}

// splitDomain splits "example.co.uk" into SLD "example" and TLD "co.uk".
func splitDomain(domain string) (string, string) {
	for i := 0; i < len(domain); i++ {
		if domain[i] == '.' {
			return domain[:i], domain[i+1:]
		}
	}
	return domain, ""
}

// namecheapDate converts Namecheap's MM/DD/YYYY dates into an RFC 3339 UTC
// timestamp. It returns "" when the input is empty or malformed.
func namecheapDate(s string) string {
	s = sdk.TrimSpace(s)
	if s == "" {
		return ""
	}
	var f [3]string
	idx := 0
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '/' {
			if idx > 2 {
				return ""
			}
			f[idx] = s[start:i]
			idx++
			start = i + 1
		}
	}
	if idx != 3 {
		return ""
	}
	mm, dd, yyyy := pad2(f[0]), pad2(f[1]), f[2]
	if !allDigits(mm) || !allDigits(dd) || !allDigits(yyyy) || len(yyyy) != 4 {
		return ""
	}
	return yyyy + "-" + mm + "-" + dd + "T00:00:00Z"
}

// mapInfoStatus maps Namecheap's domain status onto the contract vocabulary.
func mapInfoStatus(status string) string {
	switch sdk.ToLower(sdk.TrimSpace(status)) {
	case "expired":
		return "expired"
	default:
		return "active"
	}
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
