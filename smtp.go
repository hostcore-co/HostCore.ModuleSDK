package sdk

import "github.com/extism/go-pdk"

// SMTPResponse is the result of an outbound e-mail send.
type SMTPResponse struct {
	// OK is true when the message was accepted by the SMTP server.
	OK bool
	// Error carries the failure reason when OK is false.
	Error string
}

// smtp_send is exported by the HostCore Module Engine for email modules. It is
// the only way to send mail; like the http_* functions it takes a pointer to a
// JSON request and returns a pointer to a JSON response.
//
// The request JSON has the shape:
//
//	{
//	  "host": "smtp.example.com", "port": 587,
//	  "username": "...", "password": "...", "encryption": "tls",
//	  "from_email": "no-reply@example.com", "from_name": "HostCore",
//	  "to": ["user@example.com"], "cc": ["..."], "reply_to": "...",
//	  "subject": "...", "html": "<p>...</p>", "text": "..."
//	}
//
// The response JSON is {"ok": true} on success or
// {"ok": false, "error": "..."} on failure.
//
//go:wasmimport extism:host/user smtp_send
func _smtpSend(ptr uint64) uint64

// SMTPSend sends an e-mail through the HostCore host function. The request must
// be the JSON document described on _smtpSend.
func SMTPSend(request []byte) SMTPResponse {
	mem := pdk.AllocateBytes(request)
	resultOffset := _smtpSend(mem.Offset())
	if resultOffset == 0 {
		return SMTPResponse{OK: false, Error: "smtp host function returned null"}
	}
	resultMem := pdk.FindMemory(resultOffset)
	resultBytes := make([]byte, resultMem.Length())
	resultMem.Load(resultBytes)
	return SMTPResponse{
		OK:    extractBoolField(resultBytes, "ok"),
		Error: GetBytes(resultBytes, "error").String(),
	}
}
