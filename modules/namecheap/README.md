# Namecheap domain module

A reference HostCore **domain module** for the Namecheap registrar. Domain
modules register, transfer, renew and manage domains rather than provisioning
services, so they expose a domain-lifecycle surface instead of the
`DeployService`/`SuspendService` set. This is the template for other registrar
modules (Enom, ResellerClub, …).

Namecheap speaks XML over a single GET endpoint (`xml.response`) with the command
selected by a query parameter; every response is wrapped in
`<ApiResponse Status="OK|ERROR">`. Like every module here it imports **only the
HostCore.ModuleSDK** — it reads responses with the SDK's XML reader
(`sdk.ParseXML`) rather than `encoding/xml`, and builds requests with the SDK's
string helpers.

## Exported functions → Namecheap command

| Function | Namecheap command |
| --- | --- |
| `TestModuleConnection` | `namecheap.domains.getList` |
| `CheckDomainAvailability` | `namecheap.domains.check` |
| `RegisterDomain` | `namecheap.domains.create` (+ `getInfo` for the expiry) |
| `TransferDomain` | `namecheap.domains.transfer.create` |
| `RenewDomain` | `namecheap.domains.renew` |
| `GetDomainInfo` | `namecheap.domains.getInfo` |
| `UpdateNameservers` | `namecheap.domains.dns.setCustom` |
| `SetDomainLock` | `namecheap.domains.setRegistrarLock` |
| `GetDomainContacts` | `namecheap.domains.getContacts` |
| `UpdateDomainContacts` | `namecheap.domains.setContacts` |
| `GetDomainEPPCode` | **unsupported** — Namecheap emails the auth code to the registrant; the module returns `{"code":"unsupported"}` |

There is no config-options function: the binding's connection schema lives in
[`namecheap-module.json`](namecheap-module.json).

## Configuration

The connection fields (see the module document) are the binding configuration:

| Key | Type | Notes |
| --- | --- | --- |
| `api_user` | string | Namecheap API username. |
| `api_key` | secret | Namecheap API key. |
| `username` | string | Account username (usually the same as `api_user`). |
| `client_ip` | string | A whitelisted public IPv4 — see below. |
| `sandbox` | bool | Use `api.sandbox.namecheap.com`. |

### The `client_ip` field

Namecheap requires **two** IP steps, and both are real:

1. **`ClientIp` request parameter** — one of the five required global parameters
   (`ApiUser`, `ApiKey`, `UserName`, `ClientIp`, `Command`) sent on *every* API
   call. The module sends `client_ip` as this value.
2. **Dashboard whitelist** — the *source* IP of the call must be whitelisted
   under **Profile → Tools → API Access**. Namecheap rejects any call from an
   unlisted IP with `Invalid request IP`. In HostCore that source is the Module
   Engine's outbound/exit-node IP, so `client_ip` should be that same
   whitelisted public IPv4.

## Notes on the Namecheap API

- Responses are XML; the Module Engine escapes non-JSON bodies into a JSON
  string, which the client unwraps before parsing with `sdk.ParseXML`.
- A domain the account does not hold makes `getInfo` return an API error; the
  module maps that to `{"exists": false}`.
- Dates come back as `MM/DD/YYYY` and are converted to RFC 3339.
- One submitted contact set is fanned out to all four Namecheap contact roles
  (Registrant, Tech, Admin, AuxBilling).

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/namecheap modules/namecheap/namecheap.wasm
make -C ../.. test      # runs the mock-Namecheap verification harness
```
