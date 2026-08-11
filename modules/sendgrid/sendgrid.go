// HostCore Service Module — SendGrid
//
// This is a reference HostCore email module that sends transactional mail
// through the SendGrid v3 API (https://docs.sendgrid.com/api-reference/mail-send/mail-send).
// Unlike the SMTP module, delivery is a plain HTTPS request, so it uses the
// SDK's http_* host functions rather than smtp_send.
//
// It exports a single function, SendEmail.
//
// # Input contract
//
//	{
//	  "api_key":    "SG.xxxxx",            // SendGrid API key
//	  "from_email": "no-reply@example.com",
//	  "from_name":  "HostCore",
//	  "to":         "user@example.com",    // string or array of addresses
//	  "cc":         ["ops@example.com"],   // optional
//	  "reply_to":   "support@example.com", // optional
//	  "subject":    "Welcome",
//	  "html":       "<p>Hello</p>",        // html body (optional if text set)
//	  "text":       "Hello"                // plain-text body (optional if html set)
//	}
//
// The api_key and from fields are the binding configuration; the recipients,
// subject and body are supplied per call.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// SendEmail delivers a single message through the SendGrid v3 API.
//
//go:wasmexport SendEmail
func SendEmail() int32 {
	args := sdk.GetArgs()

	apiKey := sdk.TrimSpace(args.Get("api_key").String())
	if apiKey == "" {
		return sdk.Fail("api_key is required.", nil)
	}
	from := sdk.TrimSpace(args.Get("from_email").String())
	if from == "" {
		return sdk.Fail("from_email is required.", nil)
	}
	to := sdk.StringList(args.Get("to"))
	if len(to) == 0 {
		return sdk.Fail("at least one recipient (to) is required.", nil)
	}

	html := args.Get("html").String()
	text := args.Get("text").String()
	if html == "" && text == "" {
		return sdk.Fail("an email body (html or text) is required.", nil)
	}

	// personalizations[0] carries the recipients.
	personalization := sdk.NewJSON().Set("to", emailArray(to))
	if cc := sdk.StringList(args.Get("cc")); len(cc) > 0 {
		personalization.Set("cc", emailArray(cc))
	}

	fromObj := sdk.NewJSON().Set("email", from)
	if name := sdk.TrimSpace(args.Get("from_name").String()); name != "" {
		fromObj.Set("name", name)
	}

	body := sdk.NewJSON().
		Set("personalizations", wrapArray(personalization.Build())).
		Set("from", fromObj.Build()).
		Set("subject", args.Get("subject").String()).
		Set("content", contentArray(text, html))
	if replyTo := sdk.TrimSpace(args.Get("reply_to").String()); replyTo != "" {
		body.Set("reply_to", sdk.NewJSON().Set("email", replyTo).Build())
	}

	resp := sdk.HTTPPost("https://api.sendgrid.com/v3/mail/send", map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Content-Type":  "application/json",
	}, body.Build())

	if !resp.OK {
		reason := sdk.GetBytes(resp.Body, "errors.0.message").String()
		if reason == "" {
			reason = "status " + sdk.Itoa(resp.Status)
		}
		return sdk.Fail("Failed to send the email ("+reason+").", nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("sent", true).
		Set("recipients", len(to)).
		Build())
}

// GetServiceConfigOptions returns the schema of the binding's SendGrid
// connection fields. HostCore uses this to render the configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"API Key","type":"secret","key":"api_key","description":"Your SendGrid API key.","regex":"","required":true},
		{"order":2,"label":"From Email","type":"string","key":"from_email","description":"Address messages are sent from (must be a verified sender).","regex":"^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$","required":true},
		{"order":3,"label":"From Name","type":"string","key":"from_name","description":"Display name messages are sent from.","regex":"","required":false}
	]`))
}

// emailArray builds a JSON array of SendGrid address objects,
// e.g. ["a@x","b@x"] → [{"email":"a@x"},{"email":"b@x"}].
func emailArray(addrs []string) []byte {
	items := make([][]byte, 0)
	for _, a := range addrs {
		items = append(items, sdk.NewJSON().Set("email", a).Build())
	}
	return jsonArray(items)
}

// contentArray builds SendGrid's content array. SendGrid requires the plain
// text part to appear before the html part.
func contentArray(text, html string) []byte {
	items := make([][]byte, 0)
	if text != "" {
		items = append(items, part("text/plain", text))
	}
	if html != "" {
		items = append(items, part("text/html", html))
	}
	return jsonArray(items)
}

func part(mime, value string) []byte {
	return sdk.NewJSON().Set("type", mime).Set("value", value).Build()
}

// wrapArray wraps a single raw JSON object in a one-element array.
func wrapArray(obj []byte) []byte {
	return jsonArray([][]byte{obj})
}

// jsonArray joins pre-encoded JSON values into an array.
func jsonArray(items [][]byte) []byte {
	out := make([]byte, 0)
	out = append(out, '[')
	for i, it := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, it...)
	}
	out = append(out, ']')
	return out
}
