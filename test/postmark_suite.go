// Postmark email module scenarios. The module posts to the Postmark email API
// over HTTP, so the suite mocks that endpoint and asserts on the request body
// (From formatting, comma-joined recipients, bodies, stream) and the ErrorCode
// success contract.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type postmarkMock struct {
	last     map[string]any
	token    string
	failBody string
}

func (m *postmarkMock) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	if method == "POST" && strings.HasSuffix(u.Path, "/email") {
		m.last = body
		if m.failBody != "" {
			return 422, m.failBody
		}
		return 200, `{"ErrorCode":0,"Message":"OK","MessageID":"abc-123"}`
	}
	return 404, `{}`
}

func runPostmark(wasm []byte) {
	postmarkSend(wasm)
	postmarkValidation(wasm)
	postmarkFailure(wasm)
	postmarkConfigOptions(wasm)
}

func pmArgs(fields string) string {
	return `{"api_key":"pm-token","from_email":"no-reply@hostcore.test","from_name":"HostCore",` + fields + `}`
}

func postmarkSend(wasm []byte) {
	fmt.Println("── SendEmail posts a well-formed email request")
	m := &postmarkMock{}
	plugin := loadPlugin(wasm, m.route)

	res := call(plugin, "SendEmail", pmArgs(`"to":["a@x.test","b@x.test"],"cc":"ops@x.test","reply_to":"r@x.test","subject":"Hi","html":"<p>H</p>","text":"H"`))
	assert("send succeeds", res["success"] == true, res)

	b := m.last
	assert("From combines name and address", b["From"] == "HostCore <no-reply@hostcore.test>", b["From"])
	assert("To comma-joined", b["To"] == "a@x.test,b@x.test", b["To"])
	assert("Cc forwarded", b["Cc"] == "ops@x.test", b["Cc"])
	assert("ReplyTo forwarded", b["ReplyTo"] == "r@x.test", b["ReplyTo"])
	assert("HtmlBody forwarded", b["HtmlBody"] == "<p>H</p>", b["HtmlBody"])
	assert("TextBody forwarded", b["TextBody"] == "H", b["TextBody"])
	assert("MessageStream defaults to outbound", b["MessageStream"] == "outbound", b["MessageStream"])

	data, _ := res["data"].(map[string]any)
	assert("message id returned", data["message_id"] == "abc-123", data["message_id"])
	assert("reports 2 recipients", toInt(data["recipients"]) == 2, data["recipients"])
}

func postmarkValidation(wasm []byte) {
	fmt.Println("── SendEmail validates its inputs")
	m := &postmarkMock{}
	plugin := loadPlugin(wasm, m.route)

	assert("missing token fails", call(plugin, "SendEmail",
		`{"from_email":"a@x.test","to":"b@x.test","text":"y","subject":"s"}`)["success"] == false, nil)
	assert("missing recipients fails", call(plugin, "SendEmail",
		pmArgs(`"subject":"s","text":"y"`))["success"] == false, nil)
	assert("missing body fails", call(plugin, "SendEmail",
		pmArgs(`"to":"a@x.test","subject":"s"`))["success"] == false, nil)
	assert("nothing posted on validation failure", m.last == nil, m.last)
}

func postmarkFailure(wasm []byte) {
	fmt.Println("── SendEmail surfaces API errors (non-zero ErrorCode)")
	m := &postmarkMock{failBody: `{"ErrorCode":300,"Message":"Invalid 'To' address"}`}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "SendEmail", pmArgs(`"to":"a@x.test","subject":"s","text":"y"`))
	assert("send reports failure", res["success"] == false, res)
	assert("failure carries API message", strings.Contains(str(res["error"]), "Invalid 'To' address"), res["error"])

	// A 200 response with a non-zero ErrorCode must also be treated as failure.
	m2 := &postmarkMock{}
	m2.failBody = ""
	// craft a 200 + non-zero ErrorCode by overriding the success body.
	plugin2 := loadPlugin(wasm, func(method, rawURL string, body map[string]any) (int, string) {
		return 200, `{"ErrorCode":10,"Message":"Inactive recipient"}`
	})
	res2 := call(plugin2, "SendEmail", pmArgs(`"to":"a@x.test","subject":"s","text":"y"`))
	assert("200 with non-zero ErrorCode is a failure", res2["success"] == false, res2)
	_ = m2
}

func postmarkConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	m := &postmarkMock{}
	plugin := loadPlugin(wasm, m.route)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)
	data, _ := res["data"].([]any)
	var tokenSecret bool
	for _, f := range data {
		fm, _ := f.(map[string]any)
		if fm["key"] == "api_key" {
			tokenSecret = fm["type"] == "secret" && fm["required"] == true
		}
	}
	assert("server token is a required secret", tokenSecret, nil)
}
