# Convoy module

A HostCore service module for the [Convoy](https://convoypanel.com) KVM server
panel, which wraps Proxmox to sell virtual machines. It provisions and manages
servers through the Convoy **Application API v4** — the same surface the
[Paymenter Convoy extension](https://paymenter.org/docs/extensions/convoy)
drives.

## Two credentials, two lifetimes

Convoy separates the panel account from the machine, and so does this module:

| Credential | Belongs to | Set by | Changed by |
| --- | --- | --- | --- |
| Panel password | The customer's one Convoy user | `DeployService`, on first deploy | `ChangePassword` |
| Root password | Each server | `DeployService`, as `account_password` at build time | Nothing — Convoy exposes no endpoint |

`DeployService` reports the **root** login, because that is what the customer
uses to reach the machine HostCore just sold them. The panel account is created
once and reused for every later server that customer orders.

`ChangePassword` therefore resets the **panel** password, not the machine's.
That matches the VirtFusion module, where the same function resets the panel
login rather than the server's.

Convoy enforces 8–50 characters spanning an upper-case letter, a lower-case
letter, a digit and a symbol. Generated passwords come from `sdk.Password`,
which draws on the host's cryptographic source and guarantees that shape.

## Server UUID round-trip

Convoy addresses a server by the **UUID** it mints at create time.
`DeployService` returns it as `server_id` (and as `uuid`), HostCore persists it,
and it comes back as `remote_id` on every later lifecycle call. Those calls fail
cleanly when it is absent.

## Addresses

Addresses are drawn from the node's pool rather than assigned by the plan: the
module asks for the unallocated addresses of each family
(`filter[server_id]=` empty) and hands their ids to the build. A plan that asks
for more addresses than the node has free **fails the deploy** rather than
quietly building a server with fewer than the customer paid for.

`ipv4` defaults to **1** when the plan leaves it blank — a server with no
address is unreachable, which is never what a blank field means to order. Set it
to `0` explicitly for a server that should have none.

Convoy replaces a server's address list wholesale on a build change, so
`UpdateService` reads back the addresses the server already holds, carries them
through, and draws only the shortfall when a plan raises a count. Lowering a
count does not release addresses.

## Exported functions → Convoy API

| Function | Convoy Application API |
| --- | --- |
| `TestModuleConnection` | `GET /nodes` |
| `DeployService` | `GET /users?filter[email]=` (or `POST /users`), `GET /nodes/{id}/addresses`, then `POST /servers` |
| `SuspendService` | `POST /servers/{uuid}/settings/suspend` |
| `UnsuspendService` | `POST /servers/{uuid}/settings/unsuspend` |
| `TerminateService` | `DELETE /servers/{uuid}` |
| `UpdateService` | `GET /servers/{uuid}` then `PATCH /servers/{uuid}/settings/build` |
| `ChangePassword` | `GET /users/{id}` then `PUT /users/{id}` |
| `ServiceAction` | **unsupported** — see below |

A UUID Convoy no longer holds terminates successfully; the server is gone either
way.

### Why `ServiceAction` is unsupported

Convoy keeps power control on its **client** API, which answers to a customer's
own token — not the application token this module holds. There is no
start/stop/restart endpoint on the application API to call, so the function
fails with `{"code": "unsupported"}` rather than pretending. Customers power
their servers from the Convoy panel itself.

Convoy does offer single sign-on
(`POST /users/{id}/generate-sso-token` → `/authenticate?token=…`), which is how
the Paymenter extension sends a customer into the panel. HostCore's service
module surface has no function for handing back a login link, so this module
does not expose it; adding one would let a Convoy customer reach their panel in
one click.

## E-mail matching

Convoy's `filter[email]` is a **partial** match, so a lookup for
`a@b.com` can return an account merely containing that string. The module checks
the returned rows for the exact address (case-insensitively) instead of taking
the first row, which is what the Paymenter extension does.

## Input

| Field | Purpose |
| --- | --- |
| `domain` | Convoy panel hostname (no scheme). |
| `api_key` | Application API token, sent as `Authorization: Bearer`. |
| `remote_id` | Server UUID, supplied on lifecycle calls after deploy. |
| `service_id` | Store service id; names the hostname when none is configured. |
| `password` | Root password for the server. A compliant one is generated when absent. |
| `user` | `{ id, email, first_name, last_name }`. The e-mail resolves the panel account. |

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `node_id` | number | **Required.** Convoy node to deploy on. |
| `template_uuid` | string | **Required.** The template's own UUID — *not* its group's. |
| `cpu` | number | **Required.** Virtual cores. |
| `memory`, `disk` | number | **Required.** In **MiB**; stored by Convoy as bytes. |
| `bandwidth` | number | Monthly allowance in MiB. Blank or 0 is unmetered (`null`). |
| `snapshots` | number | Snapshot allowance. |
| `backups` | number | Backup allowance. Blank is unlimited (`null`). |
| `ipv4`, `ipv6` | number | Addresses to draw from the node's pool. `ipv4` defaults to 1. |
| `hostname` | string | The server's hostname. Derived from the service id when blank. |
| `start_on_completion` | bool | Power on once built. Defaults to on. |

Sizes are written in **MiB** because that is the unit a plan is priced in;
the module multiplies by 1 MiB before sending. Those products exceed a 32-bit
integer for any ordinary disk, so they are carried as `int64` throughout — a
50 GiB disk is 53,687,091,200 bytes.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/convoy modules/convoy/convoy.wasm
make -C ../.. test      # runs the mock-Convoy verification harness
```
