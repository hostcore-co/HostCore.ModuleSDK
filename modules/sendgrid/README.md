# SendGrid module

A reference HostCore **email module** that sends transactional mail through the
[SendGrid v3 API](https://docs.sendgrid.com/api-reference/mail-send/mail-send).
Unlike the [SMTP module](../smtp), delivery is an ordinary HTTPS request, so it
uses the SDK's `http_*` host functions — no `smtp_send` host function required.

## Exported functions

| Function | Behaviour |
| --- | --- |
| `SendEmail` | Validates inputs, builds a `mail/send` request and posts it to SendGrid. |
| `GetServiceConfigOptions` | Returns the binding's connection schema. |

## Input

The `api_key` and `from` fields are the binding configuration; the recipients,
subject and body are supplied per call.

| Field | Purpose |
| --- | --- |
| `api_key` | SendGrid API key (sent as `Authorization: Bearer`). |
| `from_email`, `from_name` | Verified sender. |
| `to` | Recipient(s): a string or an array of addresses. |
| `cc`, `reply_to` | Optional. `cc` accepts a string or array. |
| `subject` | Message subject. |
| `html`, `text` | Message body. At least one is required. |

`to` and `cc` accept a single string or an array; addresses are trimmed and
de-duplicated. Both body parts are sent when supplied, with the plain-text part
ordered before the HTML part as SendGrid requires.

## Success

SendGrid returns `202 Accepted` on success. On failure the module surfaces the
first `errors[].message` from the response.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/sendgrid modules/sendgrid/sendgrid.wasm
make -C ../.. test      # runs the verification harness (mocks the SendGrid API)
```
