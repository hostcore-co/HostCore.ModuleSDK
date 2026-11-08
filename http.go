package sdk

import "github.com/extism/go-pdk"

// HTTPResponse is the result of an outbound HTTP call.
type HTTPResponse struct {
	// Status is the HTTP status code returned by the upstream.
	Status int
	// Ok is true when Status is in the 200 range.
	OK bool
	// Body is the raw response body bytes.
	Body []byte
}

// These functions are exported by the HostCore Module Engine.
// They are the only way to make a network call; all HTTP requests go through these.

//go:wasmimport extism:host/user http_GET
func _httpGet(ptr uint64) uint64

//go:wasmimport extism:host/user http_POST
func _httpPost(ptr uint64) uint64

//go:wasmimport extism:host/user http_PUT
func _httpPut(ptr uint64) uint64

//go:wasmimport extism:host/user http_PATCH
func _httpPatch(ptr uint64) uint64

//go:wasmimport extism:host/user http_DELETE
func _httpDelete(ptr uint64) uint64

// HTTPGet makes a GET request through the HostCore host function.
func HTTPGet(url string, headers map[string]string) HTTPResponse {
	req := buildHTTPRequest(url, headers, nil)
	mem := pdk.AllocateBytes(req)
	resultOffset := _httpGet(mem.Offset())
	return readHTTPResponse(resultOffset)
}

// HTTPPost makes a POST request
func HTTPPost(url string, headers map[string]string, body []byte) HTTPResponse {
	req := buildHTTPRequest(url, headers, body)
	mem := pdk.AllocateBytes(req)
	resultOffset := _httpPost(mem.Offset())
	return readHTTPResponse(resultOffset)
}

// HTTPPut makes a PUT request
func HTTPPut(url string, headers map[string]string, body []byte) HTTPResponse {
	req := buildHTTPRequest(url, headers, body)
	mem := pdk.AllocateBytes(req)
	resultOffset := _httpPut(mem.Offset())
	return readHTTPResponse(resultOffset)
}

// HTTPPatch makes a PATCH request
func HTTPPatch(url string, headers map[string]string, body []byte) HTTPResponse {
	req := buildHTTPRequest(url, headers, body)
	mem := pdk.AllocateBytes(req)
	resultOffset := _httpPatch(mem.Offset())
	return readHTTPResponse(resultOffset)
}

// HTTPDelete makes a DELETE request
func HTTPDelete(url string, headers map[string]string) HTTPResponse {
	req := buildHTTPRequest(url, headers, nil)
	mem := pdk.AllocateBytes(req)
	resultOffset := _httpDelete(mem.Offset())
	return readHTTPResponse(resultOffset)
}

func readHTTPResponse(resultOffset uint64) HTTPResponse {
	if resultOffset == 0 {
		return HTTPResponse{OK: false, Status: 0, Body: []byte(`{"error":"host function returned null"}`)}
	}
	resultMem := pdk.FindMemory(resultOffset)
	resultBytes := make([]byte, resultMem.Length())
	resultMem.Load(resultBytes)
	return parseHTTPResponse(resultBytes)
}

// buildHTTPRequest constructs the HTTPRequest JSON
func buildHTTPRequest(url string, headers map[string]string, body []byte) []byte {
	var b []byte
	b = append(b, `{"url":`...)
	b = append(b, JsonString(url)...)

	if len(headers) > 0 {
		b = append(b, `,"headers":{`...)
		first := true
		for k, v := range headers {
			if !first {
				b = append(b, ',')
			}
			b = append(b, JsonString(k)...)
			b = append(b, ':')
			b = append(b, JsonString(v)...)
			first = false
		}
		b = append(b, '}')
	}

	if len(body) > 0 {
		b = append(b, `,"body":`...)
		b = append(b, body...)
	}

	b = append(b, '}')
	return b
}

// parseHTTPResponse extracts status, ok, and body from the response JSON
func parseHTTPResponse(data []byte) HTTPResponse {
	resp := HTTPResponse{}

	// Extract "status" field
	resp.Status = extractIntField(data, "status")
	resp.OK = extractBoolField(data, "ok")

	// Extract the "body" field — a nested JSON value that is the last field of
	// the wrapper object, so it runs from bodyStart to the wrapper's closing
	// brace. That final brace must be dropped: leaving it in place produces
	// invalid JSON when a module embeds Body into its own result via
	// Success/Fail.
	bodyStart := findField(data, `"body":`)
	if bodyStart >= 0 {
		end := len(data)
		if end > bodyStart && data[end-1] == '}' {
			end--
		}
		resp.Body = data[bodyStart:end]
	}

	return resp
}

// extractIntField finds a numeric field value in raw JSON bytes.
func extractIntField(data []byte, field string) int {
	key := `"` + field + `":`
	idx := bytesIndex(data, []byte(key))
	if idx < 0 {
		return 0
	}
	idx += len(key)
	// skip whitespace
	for idx < len(data) && (data[idx] == ' ' || data[idx] == '\t') {
		idx++
	}
	n := 0
	for idx < len(data) && data[idx] >= '0' && data[idx] <= '9' {
		n = n*10 + int(data[idx]-'0')
		idx++
	}
	return n
}

// extractBoolField finds a boolean field value in raw JSON bytes.
func extractBoolField(data []byte, field string) bool {
	key := `"` + field + `":true`
	return bytesIndex(data, []byte(key)) >= 0
}

// findField returns the start index of the value for a given field key.
func findField(data []byte, key string) int {
	idx := bytesIndex(data, []byte(key))
	if idx < 0 {
		return -1
	}
	return idx + len(key)
}

// bytesIndex is bytes.Index without importing bytes (to keep binary small).
func bytesIndex(s, sep []byte) int {
	if len(sep) == 0 {
		return 0
	}
	if len(sep) > len(s) {
		return -1
	}
outer:
	for i := 0; i <= len(s)-len(sep); i++ {
		for j := 0; j < len(sep); j++ {
			if s[i+j] != sep[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
