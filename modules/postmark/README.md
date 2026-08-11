# Postmark module

A reference HostCore **email module** that sends transactional mail through the
[Postmark email API](https://postmarkapp.com/developer/api/email-api). Like the
[SendGrid module](../sendgrid), delivery is an ordinary HTTPS request over the
SDK's `http_*` host functions.

## Exported functions

| Function | Behaviour |
| --- | --- |
| `SendEmail` | Validates inputs, builds the request and posts it to Postmark. |
| `GetServiceConfigOptions` | Returns the binding's connection schema. |

## Input

The `api_key` and `from` fields are the binding configuration; the recipients,
subject and body are supplied per call.

| Field | Purpose |
| --- | --- |
| `api_key` | Postmark **server token** (sent as `X-Postmark-Server-Token`). |
| `from_email`, `from_name` | Confirmed sender signature. Combined as `Name <email>`. |
| `message_stream` | Optional. Defaults to `outbound`. |
| `to` | Recipient(s): a string or an array of addresses. |
| `cc`, `reply_to` | Optional. `cc` accepts a string or array. |
| `subject` | Message subject. |
| `html`, `text` | Message body (`HtmlBody` / `TextBody`). At least one is required. |

`to` and `cc` accept a single string or an array; addresses are trimmed,
de-duplicated and joined into the comma-separated lists Postmark expects.

## Success

Postmark reports success with HTTP `200` **and** `ErrorCode: 0`; the module
requires both. On failure it surfaces the response `Message`. On success it
returns the Postmark `MessageID`.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/postmark modules/postmark/postmark.wasm
make -C ../.. test      # runs the verification harness (mocks the Postmark API)
```
