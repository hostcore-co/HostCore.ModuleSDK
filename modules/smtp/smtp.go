// HostCore Service Module — SMTP
//
// This is a reference HostCore email module. Unlike the service modules, which
// provision accounts on an external panel, an email module sends transactional
// mail. It exports a single function, SendEmail, which delivers a message
// through the SMTP server configured on the binding.
//
// Mail is sent through the SDK's smtp_send host function — the email-module
// counterpart to the http_* host functions. The Module Engine must expose
// smtp_send for this module to run; there is no other network path from the
// sandbox.
//
// # Input contract
//
//	{
//	  "host":       "smtp.example.com",   // SMTP server host
//	  "port":       587,                  // SMTP port (default 587)
//	  "username":   "apikey",             // SMTP auth username
//	  "password":   "secret",             // SMTP auth password
//	  "encryption": "tls",                // tls | ssl | starttls | none
//	  "from_email": "no-reply@example.com",
//	  "from_name":  "HostCore",
//	  "to":         "user@example.com",   // string or array of addresses
//	  "cc":         ["ops@example.com"],  // optional
//	  "reply_to":   "support@example.com",// optional
//	  "subject":    "Welcome",
//	  "html":       "<p>Hello</p>",       // html body (optional if text set)
//	  "text":       "Hello"               // plain-text body (optional if html set)
//	}
//
// The host, port, credentials and from fields are the binding configuration;
// the recipients, subject and body are supplied per call.
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

// SendEmail delivers a single message through the configured SMTP server.
//
//go:wasmexport SendEmail
func SendEmail() int32 {
	args := sdk.GetArgs()

	host := sdk.TrimSpace(args.Get("host").String())
	if host == "" {
		return sdk.Fail("host is required.", nil)
	}

	to := sdk.StringList(args.Get("to"))
	if len(to) == 0 {
		return sdk.Fail("at least one recipient (to) is required.", nil)
	}

	subject := args.Get("subject").String()
	html := args.Get("html").String()
	text := args.Get("text").String()
	if html == "" && text == "" {
		return sdk.Fail("an email body (html or text) is required.", nil)
	}

	port := args.Get("port").Int()
	if port == 0 {
		port = 587
	}
	encryption := sdk.TrimSpace(args.Get("encryption").String())
	if encryption == "" {
		encryption = "tls"
	}

	msg := sdk.NewJSON().
		Set("host", host).
		Set("port", port).
		Set("username", args.Get("username").String()).
		Set("password", args.Get("password").String()).
		Set("encryption", encryption).
		Set("from_email", args.Get("from_email").String()).
		Set("from_name", args.Get("from_name").String()).
		Set("to", sdk.JSONStringArray(to)).
		Set("subject", subject).
		Set("html", html).
		Set("text", text)

	if cc := sdk.StringList(args.Get("cc")); len(cc) > 0 {
		msg.Set("cc", sdk.JSONStringArray(cc))
	}
	if replyTo := sdk.TrimSpace(args.Get("reply_to").String()); replyTo != "" {
		msg.Set("reply_to", replyTo)
	}

	resp := sdk.SMTPSend(msg.Build())
	if !resp.OK {
		reason := ""
		if resp.Error != "" {
			reason = " (" + resp.Error + ")"
		}
		return sdk.Fail("Failed to send the email"+reason+".", nil)
	}

	return sdk.Success(sdk.NewJSON().
		Set("sent", true).
		Set("recipients", len(to)).
		Build())
}

// GetServiceConfigOptions returns the schema of the binding's SMTP connection
// fields. HostCore uses this to render the configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"SMTP Host","type":"string","key":"host","description":"Hostname of the SMTP server.","regex":"","required":true},
		{"order":2,"label":"SMTP Port","type":"number","key":"port","description":"SMTP port. Defaults to 587.","regex":"^[0-9]+$","required":false},
		{"order":3,"label":"Username","type":"string","key":"username","description":"SMTP authentication username.","regex":"","required":true},
		{"order":4,"label":"Password","type":"secret","key":"password","description":"SMTP authentication password.","regex":"","required":true},
		{"order":5,"label":"Encryption","type":"string","key":"encryption","description":"Transport encryption: tls, ssl, starttls or none. Defaults to tls.","regex":"^(tls|ssl|starttls|none)$","required":false},
		{"order":6,"label":"From Email","type":"string","key":"from_email","description":"Address messages are sent from.","regex":"^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$","required":true},
		{"order":7,"label":"From Name","type":"string","key":"from_name","description":"Display name messages are sent from.","regex":"","required":false}
	]`))
}
