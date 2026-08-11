// Enom reseller API client for the domain module. Enom exposes a single GET
// endpoint (interface.asp) with the command in the query string; requesting
// responsetype=xml returns an <interface-response> envelope. This file owns
// request building, the shared error handling and the value helpers.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

const (
	liveBase = "https://reseller.enom.com/interface.asp"
	testBase = "https://resellertest.enom.com/interface.asp"
)

// enom holds the store's Enom credentials, resolved once per call from the
// binding configuration injected into the args.
type enom struct {
	uid  string
	pw   string
	base string
}

func newClient(args sdk.Args) enom {
	base := liveBase
	if sdk.ParseBool(args.Get("test").String()) {
		base = testBase
	}
	return enom{
		uid:  sdk.TrimSpace(args.Get("uid").String()),
		pw:   sdk.TrimSpace(args.Get("pw").String()),
		base: base,
	}
}

// call issues an Enom command and returns the parsed <interface-response> root.
// The message is non-empty when the transport failed or the envelope reported
// errors (ErrCount > 0), so callers can fail the module cleanly.
func (c enom) call(command string, params [][2]string) (sdk.XMLNode, string) {
	url := c.base +
		"?responsetype=xml" +
		"&uid=" + sdk.URLEncode(c.uid) +
		"&pw=" + sdk.URLEncode(c.pw) +
		"&command=" + sdk.URLEncode(command)
	for _, p := range params {
		url += "&" + sdk.URLEncode(p[0]) + "=" + sdk.URLEncode(p[1])
	}

	resp := sdk.HTTPGet(url, map[string]string{"Accept": "application/xml"})
	if !resp.OK {
		return sdk.XMLNode{}, "Enom request failed with status " + sdk.Itoa(resp.Status) + "."
	}

	// The engine escapes non-JSON bodies into a JSON string; unwrap it back to
	// the raw XML before parsing.
	root := sdk.ParseXML([]byte(sdk.ParseBytes(resp.Body).String()))
	if msg := enomError(root); msg != "" {
		return root, msg
	}
	return root, ""
}

// enomError returns the joined error text when the response reports errors, or
// "" on success. Enom sets ErrCount and lists messages as <errors><Err1>….
func enomError(root sdk.XMLNode) string {
	count := root.Get("ErrCount")
	if !count.Exists() {
		return "Enom returned an unexpected response."
	}
	n := sdk.Atoi(count.Text())
	if n <= 0 {
		return ""
	}
	errs := root.Get("errors")
	var parts []string
	for i := 1; i <= n; i++ {
		if t := errs.Child("Err" + sdk.Itoa(i)).Text(); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "Enom returned an error."
	}
	return sdk.Join(parts, "; ")
}

// expiry fetches a domain's expiry date as RFC 3339, or "" when unavailable.
func (c enom) expiry(sld, tld string) string {
	root, msg := c.call("GetDomainExp", [][2]string{{"SLD", sld}, {"TLD", tld}})
	if msg != "" {
		return ""
	}
	d := root.Get("GetDomainExp.ExpirationDate").Text()
	if d == "" {
		d = root.Get("ExpirationDate").Text()
	}
	return enomDate(d)
}

// contactParams fans one submitted contact set out to the Registrant, Tech,
// Admin and AuxBilling parameter groups Enom requires. The platform collects a
// single contact and applies it to all four roles.
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
			[2]string{role + "EmailAddress", email},
			[2]string{role + "Phone", phone},
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

// enomDate converts an Enom date into an RFC 3339 UTC timestamp. Enom returns
// M/D/YYYY optionally followed by a time (e.g. "1/15/2027 11:59:00 PM"); only
// the date is used. It returns "" when the input is empty or malformed.
func enomDate(s string) string {
	s = sdk.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Keep the date portion, dropping any trailing time.
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			s = s[:i]
			break
		}
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

// mapInfoStatus maps Enom's registration status onto the contract vocabulary.
func mapInfoStatus(status string) string {
	switch s := sdk.ToLower(sdk.TrimSpace(status)); s {
	case "expired", "redemption", "expired-redemption":
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
