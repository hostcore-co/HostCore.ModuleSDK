# Example modules

Each subdirectory is a self-contained HostCore service module: a `package main`
Go program compiled to a WASM reactor, a prebuilt `.wasm`, and a README.

| Module | Application | Notes |
| --- | --- | --- |
| [`pterodactyl`](pterodactyl) | [Pterodactyl](https://pterodactyl.io) game panel | Client-side intelligent multi-port allocation (SERVER_PORT + additional ports on one IP). |
| [`wisp`](wisp) | [WISP](https://wisp.gg) game panel | Panel-side additional-port allocation; ULID-safe ids; server templates. |
| [`cpanel`](cpanel) | [cPanel & WHM](https://cpanel.net) web hosting | Account provisioning via WHM API 1; username derivation. |
| [`plesk`](plesk) | [Plesk](https://www.plesk.com) web hosting | Subscription provisioning via the REST API v2 CLI gateway. |
| [`virtfusion`](virtfusion) | [VirtFusion](https://virtfusion.com) VPS/KVM panel | VPS provisioning via API v1; server-id round-trip; byExtRelation users. |
| [`virtualizor`](virtualizor) | [Virtualizor](https://www.virtualizor.com) VPS panel | VPS provisioning via the Admin API (act-based single endpoint). |
| [`proxmox`](proxmox) | [Proxmox VE](https://www.proxmox.com/en/proxmox-virtual-environment) hypervisor | QEMU/LXC provisioning via the API v2; token or ticket auth; background-task polling. |
| [`convoy`](convoy) | [Convoy](https://convoypanel.com) KVM panel | KVM provisioning via the Application API v4; pool-drawn addresses; byte-scaled build limits. |
| [`smtp`](smtp) | SMTP email | Email module — sends mail via the `smtp_send` host function. |
| [`sendgrid`](sendgrid) | SendGrid email | Email module — sends via the SendGrid v3 HTTP API. |
| [`postmark`](postmark) | Postmark email | Email module — sends via the Postmark HTTP API. |
| [`namecheap`](namecheap) | Namecheap registrar | Domain module — register/transfer/renew/manage over the Namecheap XML API. |
| [`enom`](enom) | Enom registrar | Domain module — register/transfer/renew/manage over the Enom reseller XML API. |

Every service module declares the customer actions its version offers, and the
ones that can carry them out export `ServiceAction`: power control where the
module's credential reaches it (Proxmox, VirtFusion, Virtualizor), single
sign-on where the panel mints a login (Convoy, VirtFusion, cPanel, Plesk), and
`reinstall` for Pterodactyl, the one server action its application API exposes.

The **service** modules export the same lifecycle surface —
`TestModuleConnection`, `GetServiceConfigOptions`, `DeployService`,
`SuspendService`, `UnsuspendService`, `UpdateService`, `TerminateService`,
`ChangePassword` and `ServiceAction` (where the target supports it). The **email** modules (`smtp`,
`sendgrid`, `postmark`) export `SendEmail`. The **domain** module (`namecheap`)
exports the domain lifecycle (`RegisterDomain`, `TransferDomain`, `RenewDomain`,
`GetDomainInfo`, …). All import only the [HostCore.ModuleSDK](..).

Each module directory also carries a `<name>-module.json` — the MongoDB module
document (metadata plus the binding's connection config schema).

## Build

```sh
make build                                  # every module
scripts/build.sh ./modules/wisp modules/wisp/wisp.wasm   # just one
```

## Add a module

1. Create `modules/<name>/<name>.go` as `package main` with an empty `func main() {}`.
2. Export the lifecycle functions with `//go:wasmexport`.
3. `make build` compiles it to `modules/<name>/<name>.wasm`.
