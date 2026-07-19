// Verification harness for the HostCore example modules.
//
// It loads each compiled WASM module exactly as the Module Engine does
// (EnableWasi, http_* host functions with the same request/response wire
// format) but routes HTTP to an in-memory mock of the target application. Each
// module has its own suite file (ptero_suite.go, wisp_suite.go, …); this file
// holds the shared extism plumbing, assertions and helpers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	extism "github.com/extism/go-sdk"
)

// router dispatches a mock HTTP request and returns (status, jsonBody). Each
// module's mock panel implements one.
type router func(method, rawURL string, body map[string]any) (int, string)

var failures int

var debug = os.Getenv("DBG") != ""

// suite pairs a module's compiled wasm path with the function that exercises it.
type suite struct {
	name string
	wasm string
	run  func(wasm []byte)
}

func main() {
	suites := []suite{
		{"pterodactyl", "../modules/pterodactyl/pterodactyl.wasm", runPtero},
		{"wisp", "../modules/wisp/wisp.wasm", runWisp},
		{"cpanel", "../modules/cpanel/cpanel.wasm", runCpanel},
		{"plesk", "../modules/plesk/plesk.wasm", runPlesk},
		{"virtfusion", "../modules/virtfusion/virtfusion.wasm", runVirtfusion},
		{"virtualizor", "../modules/virtualizor/virtualizor.wasm", runVirtualizor},
		{"smtp", "../modules/smtp/smtp.wasm", runSMTP},
	}

	for _, s := range suites {
		wasm, err := os.ReadFile(s.wasm)
		if err != nil {
			fmt.Printf("read %s wasm: %v\n", s.name, err)
			fmt.Println("build it first: make -C .. build")
			os.Exit(1)
		}
		fmt.Printf("\n========== %s ==========\n", s.name)
		s.run(wasm)
	}

	fmt.Println(strings.Repeat("=", 60))
	if failures > 0 {
		fmt.Printf("FAILED: %d assertion(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("ALL SCENARIOS PASSED")
}

// loadPlugin instantiates the module with the mock's router wired into the
// http_* host functions.
func loadPlugin(wasm []byte, route router) *extism.Plugin {
	manifest := extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: wasm}}}
	plugin, err := extism.NewPlugin(context.Background(), manifest, extism.PluginConfig{EnableWasi: true}, hostFns(route))
	if err != nil {
		fmt.Println("plugin load:", err)
		os.Exit(1)
	}
	return plugin
}

// hostFns builds the http_* host functions, mirroring the engine's
// functions/http.go request/response wire format.
func hostFns(route router) []extism.HostFunction {
	fn := func(method string) extism.HostFunction {
		return extism.NewHostFunctionWithStack("http_"+method,
			func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
				in, _ := plugin.ReadBytes(stack[0])
				var req struct {
					URL     string          `json:"url"`
					Headers map[string]any  `json:"headers"`
					Body    json.RawMessage `json:"body"`
				}
				_ = json.Unmarshal(in, &req)
				var body map[string]any
				if len(req.Body) > 0 {
					_ = json.Unmarshal(req.Body, &body)
				}
				status, respBody := route(method, req.URL, body)
				off, _ := plugin.WriteBytes([]byte(buildResp(status, respBody)))
				stack[0] = off
			},
			[]extism.ValueType{extism.ValueTypePTR},
			[]extism.ValueType{extism.ValueTypePTR})
	}
	// smtp_send is the email-module host function. It is provided to every
	// plugin (unused ones ignore it) and records the outbound message so the
	// SMTP suite can assert on it.
	smtp := extism.NewHostFunctionWithStack("smtp_send",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			in, _ := plugin.ReadBytes(stack[0])
			var msg map[string]any
			_ = json.Unmarshal(in, &msg)
			smtpInbox = append(smtpInbox, msg)
			out := `{"ok":true}`
			if smtpReject != "" {
				out = fmt.Sprintf(`{"ok":false,"error":%q}`, smtpReject)
			}
			off, _ := plugin.WriteBytes([]byte(out))
			stack[0] = off
		},
		[]extism.ValueType{extism.ValueTypePTR},
		[]extism.ValueType{extism.ValueTypePTR})

	return []extism.HostFunction{fn("GET"), fn("POST"), fn("PUT"), fn("PATCH"), fn("DELETE"), smtp}
}

// smtpInbox collects messages sent through the mock smtp_send host function;
// smtpReject, when non-empty, makes the next sends fail with that reason.
var (
	smtpInbox  []map[string]any
	smtpReject string
)

func buildResp(status int, body string) string {
	ok := status >= 200 && status < 300
	if body == "" {
		body = `""`
	}
	if !json.Valid([]byte(body)) {
		b, _ := json.Marshal(body)
		body = string(b)
	}
	return fmt.Sprintf(`{"status":%d,"ok":%t,"body":%s}`, status, ok, body)
}

func call(plugin *extism.Plugin, fn, args string) map[string]any {
	exit, out, err := plugin.Call(fn, []byte(args))
	if debug {
		fmt.Printf("  [dbg] %s exit=%d err=%v out=%s\n", fn, exit, err, string(out))
	}
	if err != nil {
		fmt.Printf("  call %s error: %v\n", fn, err)
		failures++
		return nil
	}
	var res map[string]any
	_ = json.Unmarshal(out, &res)
	return res
}

func assert(name string, ok bool, detail any) {
	if ok {
		fmt.Printf("  ✓ %s\n", name)
		return
	}
	failures++
	fmt.Printf("  ✗ %s  (got: %v)\n", name, detail)
}

// baseArgs builds a module argument document with a fixed user and the given
// service_config entries (each built with cfg).
func baseArgs(extra string) string {
	return `{
		"domain":"panel.test",
		"api_key":"key_test",
		"service_id":"1024",
		"password":"secret123",
		"user":{"id":"77","email":"Alice@Example.com","first_name":"Alice","last_name":"Ng"},
		"service_config":[` + extra + `]
	}`
}

func cfg(k, v string) string { return fmt.Sprintf(`{"key":%q,"value":%q}`, k, v) }

func qstr(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func pathHead(s string) string {
	if i := strings.IndexAny(s, "?/"); i >= 0 {
		return s[:i]
	}
	return s
}

func parseRange(s string) (int, int) {
	parts := strings.SplitN(s, "-", 2)
	lo, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi := lo
	if len(parts) == 2 {
		hi, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	return lo, hi
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func toInt(v any) int     { f, _ := v.(float64); return int(f) }
func toInt64(v any) int64 { f, _ := v.(float64); return int64(f) }
