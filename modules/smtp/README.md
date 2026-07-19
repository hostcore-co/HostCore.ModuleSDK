# SMTP module

A reference HostCore **email module**. Where the service modules provision
accounts on an external panel, an email module sends transactional mail. This
one exports a single function, `SendEmail`, which delivers a message through the
SMTP server configured on the binding.

## Host function requirement

The module sandbox's only network paths are host functions. Email delivery uses
a dedicated **`smtp_send`** host function — the email-module counterpart to the
`http_*` functions — declared in the SDK (`smtp.go`). **The Module Engine must
expose `smtp_send`** for this module to run; it is a new host function, distinct
from the HTTP set the service modules use.

`smtp_send` takes a JSON request and returns `{"ok": true}` or
`{"ok": false, "error": "..."}`:

```json
{
  "host": "smtp.example.com", "port": 587,
  "username": "apikey", "password": "secret", "encryption": "tls",
  "from_email": "no-reply@example.com", "from_name": "HostCore",
  "to": ["user@example.com"], "cc": ["ops@example.com"], "reply_to": "support@example.com",
  "subject": "Welcome", "html": "<p>Hello</p>", "text": "Hello"
}
```

## Exported functions

| Function | Behaviour |
| --- | --- |
| `SendEmail` | Validates inputs, builds the message and sends it via `smtp_send`. |
| `GetServiceConfigOptions` | Returns the binding's SMTP connection schema. |

## Input

The connection fields are the binding configuration; the message fields are
supplied per call.

| Field | Purpose |
| --- | --- |
| `host`, `port`, `username`, `password`, `encryption` | SMTP connection (binding). `port` defaults to 587, `encryption` to `tls`. |
| `from_email`, `from_name` | Envelope sender (binding). |
| `to` | Recipient(s): a string or an array of addresses. |
| `cc`, `reply_to` | Optional. `cc` accepts a string or array. |
| `subject` | Message subject. |
| `html`, `text` | Message body. At least one is required. |

`to` and `cc` accept either a single string or an array; addresses are trimmed
and de-duplicated.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/smtp modules/smtp/smtp.wasm
make -C ../.. test      # runs the verification harness (mocks smtp_send)
```
