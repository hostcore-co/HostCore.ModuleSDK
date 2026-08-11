// SMTP email module scenarios. The module sends through the smtp_send host
// function (mocked in shared.go via smtpInbox / smtpReject), so the suite
// asserts on the message the module hands to the host and on its error handling.
package main

import (
	"fmt"
	"strings"
)

func runSMTP(wasm []byte) {
	smtpSend(wasm)
	smtpValidation(wasm)
	smtpFailure(wasm)
	smtpConfigOptions(wasm)
}

// smtpArgs builds an email module argument document (no service_config; the
// connection and message fields are top-level).
func smtpArgs(fields string) string {
	return `{
		"host":"smtp.test","port":587,"username":"apikey","password":"s3cret",
		"encryption":"tls","from_email":"no-reply@hostcore.test","from_name":"HostCore",
		` + fields + `
	}`
}

func smtpSend(wasm []byte) {
	fmt.Println("── SendEmail delivers a message through smtp_send")
	smtpInbox = nil
	smtpReject = ""
	plugin := loadPlugin(wasm, func(string, string, map[string]any) (int, string) { return 404, `{}` })

	args := smtpArgs(`"to":["a@x.test","b@x.test","a@x.test"],"cc":"ops@x.test","subject":"Welcome","html":"<p>Hi</p>"`)
	res := call(plugin, "SendEmail", args)
	assert("send succeeds", res["success"] == true, res)
	data, _ := res["data"].(map[string]any)
	assert("reports 2 de-duplicated recipients", toInt(data["recipients"]) == 2, data["recipients"])

	assert("one message handed to host", len(smtpInbox) == 1, len(smtpInbox))
	m := smtpInbox[0]
	assert("host forwarded", m["host"] == "smtp.test", m["host"])
	assert("port forwarded", toInt(m["port"]) == 587, m["port"])
	assert("from_email forwarded", m["from_email"] == "no-reply@hostcore.test", m["from_email"])
	assert("subject forwarded", m["subject"] == "Welcome", m["subject"])
	assert("html body forwarded", m["html"] == "<p>Hi</p>", m["html"])
	to, _ := m["to"].([]any)
	assert("recipients de-duplicated in payload", len(to) == 2, to)
	cc, _ := m["cc"].([]any)
	assert("single cc normalised to array", len(cc) == 1 && cc[0] == "ops@x.test", cc)
}

func smtpValidation(wasm []byte) {
	fmt.Println("── SendEmail validates its inputs")
	smtpInbox = nil
	smtpReject = ""
	plugin := loadPlugin(wasm, func(string, string, map[string]any) (int, string) { return 404, `{}` })

	assert("missing host fails", call(plugin, "SendEmail",
		`{"to":"a@x.test","subject":"x","text":"y"}`)["success"] == false, nil)
	assert("missing recipients fails", call(plugin, "SendEmail",
		smtpArgs(`"subject":"x","text":"y"`))["success"] == false, nil)
	assert("missing body fails", call(plugin, "SendEmail",
		smtpArgs(`"to":"a@x.test","subject":"x"`))["success"] == false, nil)
	assert("no message sent to host on validation failure", len(smtpInbox) == 0, len(smtpInbox))
}

func smtpFailure(wasm []byte) {
	fmt.Println("── SendEmail surfaces host send failures")
	smtpInbox = nil
	smtpReject = "mailbox full"
	plugin := loadPlugin(wasm, func(string, string, map[string]any) (int, string) { return 404, `{}` })

	res := call(plugin, "SendEmail", smtpArgs(`"to":"a@x.test","subject":"x","text":"y"`))
	assert("send reports failure", res["success"] == false, res)
	assert("failure carries host reason", strings.Contains(str(res["error"]), "mailbox full"), res["error"])
	smtpReject = ""
}

func smtpConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	plugin := loadPlugin(wasm, func(string, string, map[string]any) (int, string) { return 404, `{}` })
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	var passwordIsSecret, hostRequired bool
	for _, f := range data {
		m, _ := f.(map[string]any)
		switch m["key"] {
		case "password":
			passwordIsSecret = m["type"] == "secret"
		case "host":
			hostRequired = m["required"] == true
		}
	}
	assert("host required", hostRequired, nil)
	assert("password typed as secret", passwordIsSecret, nil)
}
