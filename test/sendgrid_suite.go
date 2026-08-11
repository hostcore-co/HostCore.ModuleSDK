// SendGrid email module scenarios. The module posts to the SendGrid v3 API over
// HTTP, so the suite mocks that endpoint and asserts on the request body it
// builds (personalizations, from, content ordering) and its error handling.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type sendgridMock struct {
	last   map[string]any
	reject int
}

func (m *sendgridMock) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	if method == "POST" && strings.HasSuffix(u.Path, "/v3/mail/send") {
		m.last = body
		if m.reject != 0 {
			return m.reject, `{"errors":[{"message":"invalid api key"}]}`
		}
		return 202, ``
	}
	return 404, `{}`
}

func runSendgrid(wasm []byte) {
	sendgridSend(wasm)
	sendgridValidation(wasm)
	sendgridFailure(wasm)
	sendgridConfigOptions(wasm)
}

func sgArgs(fields string) string {
	return `{"api_key":"SG.test","from_email":"no-reply@hostcore.test","from_name":"HostCore",` + fields + `}`
}

func sendgridSend(wasm []byte) {
	fmt.Println("── SendEmail posts a well-formed v3 mail/send request")
	m := &sendgridMock{}
	plugin := loadPlugin(wasm, m.route)

	res := call(plugin, "SendEmail", sgArgs(`"to":["a@x.test","b@x.test"],"cc":"ops@x.test","reply_to":"r@x.test","subject":"Hi","html":"<p>H</p>","text":"H"`))
	assert("send succeeds", res["success"] == true, res)

	b := m.last
	pers, _ := b["personalizations"].([]any)
	assert("one personalization", len(pers) == 1, pers)
	p0, _ := pers[0].(map[string]any)
	toArr, _ := p0["to"].([]any)
	assert("two to addresses as objects", len(toArr) == 2 && addrObj(toArr[0]) == "a@x.test", toArr)
	cc, _ := p0["cc"].([]any)
	assert("cc address present", len(cc) == 1 && addrObj(cc[0]) == "ops@x.test", cc)

	from, _ := b["from"].(map[string]any)
	assert("from email + name", from["email"] == "no-reply@hostcore.test" && from["name"] == "HostCore", from)

	content, _ := b["content"].([]any)
	assert("content: text/plain before text/html", len(content) == 2 && partType(content[0]) == "text/plain" && partType(content[1]) == "text/html", content)

	rt, _ := b["reply_to"].(map[string]any)
	assert("reply_to email", rt["email"] == "r@x.test", rt)
	assert("subject forwarded", b["subject"] == "Hi", b["subject"])
	data, _ := res["data"].(map[string]any)
	assert("reports 2 recipients", toInt(data["recipients"]) == 2, data["recipients"])
}

func sendgridValidation(wasm []byte) {
	fmt.Println("── SendEmail validates its inputs")
	m := &sendgridMock{}
	plugin := loadPlugin(wasm, m.route)

	assert("missing api_key fails", call(plugin, "SendEmail",
		`{"from_email":"a@x.test","to":"b@x.test","text":"y","subject":"s"}`)["success"] == false, nil)
	assert("missing recipients fails", call(plugin, "SendEmail",
		sgArgs(`"subject":"s","text":"y"`))["success"] == false, nil)
	assert("missing body fails", call(plugin, "SendEmail",
		sgArgs(`"to":"a@x.test","subject":"s"`))["success"] == false, nil)
	assert("nothing posted on validation failure", m.last == nil, m.last)
}

func sendgridFailure(wasm []byte) {
	fmt.Println("── SendEmail surfaces API errors")
	m := &sendgridMock{reject: 401}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "SendEmail", sgArgs(`"to":"a@x.test","subject":"s","text":"y"`))
	assert("send reports failure", res["success"] == false, res)
	assert("failure carries API message", strings.Contains(str(res["error"]), "invalid api key"), res["error"])
}

func sendgridConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	m := &sendgridMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	var apiKeySecret bool
	for _, f := range data {
		fm, _ := f.(map[string]any)
		if fm["key"] == "api_key" {
			apiKeySecret = fm["type"] == "secret" && fm["required"] == true
		}
	}
	assert("api_key is a required secret", apiKeySecret, nil)
}

func addrObj(v any) string {
	m, _ := v.(map[string]any)
	return str(m["email"])
}
func partType(v any) string {
	m, _ := v.(map[string]any)
	return str(m["type"])
}
