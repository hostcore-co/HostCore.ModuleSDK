# Enom domain module

A reference HostCore **domain module** for the Enom registrar — a sibling to the
[Namecheap module](../namecheap). Enom's reseller API is a single endpoint
(`interface.asp`) with the command in the query string; requesting
`responsetype=xml` returns an `<interface-response>` envelope whose `<ErrCount>`
reports success. Like every module here it imports **only the
HostCore.ModuleSDK**, reading responses with the SDK's XML reader
(`sdk.ParseXML`).

Enom addresses a domain by its second-level and top-level labels (SLD/TLD), so
the module splits the incoming full domain name (`acme.co.uk` → `acme` / `co.uk`).

## Exported functions → Enom command

| Function | Enom command |
| --- | --- |
| `TestModuleConnection` | `CheckLogin` |
| `CheckDomainAvailability` | `Check` (one call per domain; RRPCode 210 = available) |
| `RegisterDomain` | `Purchase` (+ `GetDomainExp` for the expiry, `PurchaseServices` for privacy) |
| `TransferDomain` | `TP_CreateOrder` |
| `RenewDomain` | `Extend` (+ `GetDomainExp`) |
| `GetDomainInfo` | `GetDomainInfo` + `GetDNS` + `GetRegLock` |
| `UpdateNameservers` | `ModifyNS` |
| `SetDomainLock` | `SetRegLock` (`UnlockRegistrar` 0 = lock, 1 = unlock) |
| `GetDomainContacts` | `GetContacts` |
| `UpdateDomainContacts` | `Contacts` |
| `GetDomainEPPCode` | **unsupported** — Enom emails the auth code to the registrant; the module returns `{"code":"unsupported"}` |

There is no config-options function: the binding's connection schema lives in
[`enom-module.json`](enom-module.json).

## Configuration

| Key | Type | Notes |
| --- | --- | --- |
| `uid` | string | Enom account login id. |
| `pw` | secret | Enom API password/token. |
| `test` | bool | Use the OT&E test environment (`resellertest.enom.com`). |

## Notes on the Enom API

- Responses are XML under `<interface-response>`; success is `<ErrCount>0</ErrCount>`,
  and failures list messages as `<errors><Err1>…</Err1></errors>` which the
  module joins into one error.
- Availability is read from `<RRPCode>` (210 = available, 211 = taken), not from
  `ErrCount`.
- A domain the account does not hold makes `GetDomainInfo` return an API error;
  the module maps that to `{"exists": false}`.
- Enom dates arrive as `M/D/YYYY` (sometimes with a trailing time); only the date
  is kept and converted to RFC 3339. Enom does not reliably expose a creation
  date or the privacy state, so `created_date` is empty and `privacy` is `false`
  in `GetDomainInfo`.
- One submitted contact set is fanned out to all four Enom contact roles
  (Registrant, Tech, Admin, AuxBilling).
- WHOIS privacy (ID Protect / WPPS) is a separate product, purchased best-effort
  after registration when `privacy` is requested.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/enom modules/enom/enom.wasm
make -C ../.. test      # runs the mock-Enom verification harness
```
