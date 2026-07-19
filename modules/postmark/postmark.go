// HostCore Service Module — Postmark
//
// This is a reference HostCore email module that sends transactional mail
// through the Postmark API (https://postmarkapp.com/developer/api/email-api).
// Delivery is a plain HTTPS request, so it uses the SDK's http_* host functions
// rather than smtp_send.
//
// It exports a single function, SendEmail.
//
// # Input contract
//
//	{
//	  "api_key":        "POSTMARK_SERVER_TOKEN", // Postmark server token
//	  "from_email":     "no-reply@example.com",
//	  "from_name":      "HostCore",
//	  "message_stream": "outbound",              // optional, defaults to "outbound"
//	  "to":             "user@example.com",      // string or array of addresses
//	  "cc":             ["ops@example.com"],     // optional
//	  "reply_to":       "support@example.com",   // optional
//	  "subject":        "Welcome",
//	  "html":           "<p>Hello</p>",          // html body (optional if text set)
//	  "text":           "Hello"                  // plain-text body (optional if html set)
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

// SendEmail delivers a single message through the Postmark email API.
//
//go:wasmexport SendEmail
func SendEmail() int32 {
	args := sdk.GetArgs()

	token := sdk.TrimSpace(args.Get("api_key").String())
	if token == "" {
		return sdk.Fail("api_key (server token) is required.", nil)
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

	stream := sdk.TrimSpace(args.Get("message_stream").String())
	if stream == "" {
		stream = "outbound"
	}

	body := sdk.NewJSON().
		Set("From", sender(from, sdk.TrimSpace(args.Get("from_name").String()))).
		Set("To", joinAddrs(to)).
		Set("Subject", args.Get("subject").String()).
		Set("MessageStream", stream)
	if cc := sdk.StringList(args.Get("cc")); len(cc) > 0 {
		body.Set("Cc", joinAddrs(cc))
	}
	if replyTo := sdk.TrimSpace(args.Get("reply_to").String()); replyTo != "" {
		body.Set("ReplyTo", replyTo)
	}
	if html != "" {
		body.Set("HtmlBody", html)
	}
	if text != "" {
		body.Set("TextBody", text)
	}

	resp := sdk.HTTPPost("https://api.postmarkapp.com/email", map[string]string{
		"X-Postmark-Server-Token": token,
		"Accept":                  "application/json",
		"Content-Type":            "application/json",
	}, body.Build())

	// Postmark reports success with HTTP 200 and ErrorCode 0; both must hold.
	if !resp.OK || sdk.GetBytes(resp.Body, "ErrorCode").Int() != 0 {
		reason := sdk.GetBytes(resp.Body, "Message").String()
		if reason == "" {
			reason = "status " + sdk.Itoa(resp.Status)
		}
		return sdk.Fail("Failed to send the email ("+reason+").", nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("sent", true).
		Set("recipients", len(to)).
		Set("message_id", sdk.GetBytes(resp.Body, "MessageID").String()).
		Build())
}

// GetServiceConfigOptions returns the schema of the binding's Postmark
// connection fields. HostCore uses this to render the configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Server Token","type":"secret","key":"api_key","description":"Your Postmark server token.","regex":"","required":true},
		{"order":2,"label":"From Email","type":"string","key":"from_email","description":"Address messages are sent from (must be a confirmed sender signature).","regex":"^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$","required":true},
		{"order":3,"label":"From Name","type":"string","key":"from_name","description":"Display name messages are sent from.","regex":"","required":false},
		{"order":4,"label":"Message Stream","type":"string","key":"message_stream","description":"Postmark message stream. Defaults to outbound.","regex":"","required":false}
	]`))
}

// sender formats a Postmark From header, combining the display name and address
// as "Name <email>" when a name is present.
func sender(email, name string) string {
	if name == "" {
		return email
	}
	return name + " <" + email + ">"
}

// joinAddrs renders a Postmark recipient list as a comma-separated string.
func joinAddrs(addrs []string) string {
	var out string
	for i, a := range addrs {
		if i > 0 {
			out += ","
		}
		out += a
	}
	return out
}
