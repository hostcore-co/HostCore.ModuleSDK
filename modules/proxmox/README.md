# Proxmox VE module

A HostCore service module for the [Proxmox VE](https://www.proxmox.com/en/proxmox-virtual-environment)
hypervisor. It provisions and manages QEMU virtual machines and LXC containers
through the Proxmox VE API v2 — the surface the
[Proxmox VE for WHMCS](https://github.com/The-Network-Crew/Proxmox-VE-for-WHMCS)
module drives.

## Requirements

Proxmox VE **7 or newer**. Requests carry JSON bodies, which `pvedaemon` only
decodes from that release on; a PVE 6 node answers them as though no parameters
were sent.

## Authentication

Two credential pairs are accepted, and the module prefers the first:

| Credential | How it is sent |
| --- | --- |
| `api_token_id` + `api_token_secret` | `Authorization: PVEAPIToken=user@realm!tokenid=secret` on every request. |
| `api_user` + `api_password` | `POST /access/ticket` on each execution, then the returned `PVEAuthCookie` and `CSRFPreventionToken` on every request. |

An API token is the better fit: it is stateless, so it costs no extra round
trip, and it can be scoped to a role narrower than `root@pam`. A bare
`api_user` gains the `@pam` realm. Whichever pair is used, the credential
needs `VM.Allocate`, `VM.Config.*`, `VM.PowerMgmt`, `Datastore.AllocateSpace`
and `Sys.Audit` on the guests and storage the plan touches; a token created
with privilege separation must have those granted to the token itself.

## VMID round-trip

A guest is addressed by its cluster-wide **VMID**. `DeployService` allocates
one — the cluster's next free id, or the first free id at or above the plan's
`start_vmid` — and returns it as `server_id`; HostCore persists it and replays
it as `remote_id` on every later lifecycle call.

The node is **not** trusted from the deploy. Every later call resolves the VMID
against `/cluster/resources`, so a guest that has been migrated between nodes,
or whose type the plan no longer states, is still reached correctly. The deploy
result reports the node it landed on as `hostname` for the record.

## Background tasks

Proxmox answers a mutating call with a UPID naming a background task, so the
module polls `/nodes/{node}/tasks/{upid}/status` and only reports success once
the hypervisor has finished. Engine executions are bounded, so the poll budget
is too — roughly 13 seconds.

A **failed** task fails the call, carrying Proxmox's exit status. A task still
running when the budget is spent is not an error for a deploy: the VMID is
already allocated and the disks are still copying, so the deploy succeeds with

```json
{"server_id": 105, "hostname": "pve2", "provision_pending": true, "task": "UPID:pve1:…"}
```

That binds the service to the guest Proxmox is building — failing instead would
leave a VM running with nothing on this side able to address it. When it is set,
the plan's resources have **not** been applied and the guest has not been
started; the UPID names the task to check. Full clones of large templates are
the case that reaches it, and a linked clone (`full_clone` off) avoids it.

## Exported functions → Proxmox VE API

| Function | Proxmox VE API v2 |
| --- | --- |
| `TestModuleConnection` | `GET /version` (after `POST /access/ticket` for a login) |
| `DeployService` | `GET /nodes`, `GET /cluster/nextid` or `GET /cluster/resources`, then `POST /nodes/{node}/{qemu\|lxc}` — or `POST /nodes/{node}/{type}/{template}/clone` followed by the config and start calls |
| `SuspendService` | `POST /nodes/{node}/{type}/{vmid}/status/stop` |
| `UnsuspendService` | `POST /nodes/{node}/{type}/{vmid}/status/start` |
| `TerminateService` | `status/stop` when running, then `DELETE /nodes/{node}/{type}/{vmid}?purge=1` |
| `UpdateService` | `GET` then `POST` (QEMU) or `PUT` (LXC) `/nodes/{node}/{type}/{vmid}/config`, then `PUT …/resize` |
| `ChangePassword` | `POST /nodes/{node}/qemu/{vmid}/config` with `cipassword` |

Suspend and unsuspend are no-ops when the guest is already in the target state,
and a VMID that no longer exists on the cluster terminates successfully — the
guest is gone either way.

`UpdateService` reads the guest's current `net0` before rewriting it and carries
the existing hardware address back in. Proxmox mints a fresh MAC for a device
written without one, which would cost the guest its DHCP lease. The disk resize
only ever grows a volume: Proxmox rejects a size below the current one, and the
call fails with that reason.

`ChangePassword` writes the cloud-init password, which the guest picks up on its
next boot. Proxmox exposes no password endpoint for an existing LXC container,
so the call is refused for one rather than reporting a change that never
happened.

## Input

| Field | Purpose |
| --- | --- |
| `hostname` | Proxmox host (no scheme). |
| `port` | API port. Defaults to 8006. |
| `api_token_id`, `api_token_secret` | API token credentials. |
| `api_user`, `api_password` | Ticket-login credentials, used when no token is set. |
| `remote_id` | VMID, supplied on lifecycle calls after deploy. |
| `service_id` | Store service id; names the guest when no hostname is configured. |
| `password` | Root/cloud-init password for the guest. A random one is generated when absent. |

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `guest_type` | string | **Required.** `qemu` or `lxc`. |
| `memory` | number | **Required.** Guest memory in MB. |
| `node` | string | Node to deploy on. The online node with the most free memory is chosen when blank. |
| `template_vmid` | number | Template to clone. Left blank, the guest is built from scratch. |
| `full_clone` | bool | Copy the template's disks instead of linking them. Defaults to on; a linked clone stays on the template's node. |
| `ostemplate` | string | LXC template volume. Required for `lxc` without a `template_vmid`. |
| `iso` | string | QEMU installation ISO volume, attached as `ide2`. |
| `storage`, `disk`, `disk_bus` | string, number, string | Root volume. Storage defaults to `local-lvm`, bus to `scsi`. |
| `swap` | number | LXC swap in MB. |
| `cores`, `sockets`, `cpulimit`, `cpuunits` | number | CPU allocation. Sockets default to 1 and are QEMU-only. |
| `ostype` | string | Proxmox OS type hint (`l26`, `win11`, …). |
| `bridge`, `vlan`, `netrate`, `net_model`, `firewall` | string, number, number, string, bool | Guest interface. Bridge defaults to `vmbr0`, model to `virtio`. |
| `ip_address`, `gateway`, `nameserver` | string | Addressing. `ip_address` takes CIDR form and defaults to `dhcp`; a QEMU guest gets it through cloud-init. |
| `ssh_keys` | string | Public keys authorised for the guest's root user. |
| `pool` | string | Proxmox pool to file the guest under. |
| `unprivileged` | bool | LXC only. Defaults to on. |
| `onboot`, `start_on_deploy` | bool | Start with the node, and start once provisioned. `start_on_deploy` defaults to on. |
| `start_vmid` | number | Lowest VMID this plan may allocate. |
| `hostname` | string | Guest name. Derived from the service id when blank. |

## Differences from the WHMCS module

The WHMCS module keeps its own tables — plans, IP pools, and a VMID-to-service
map — inside WHMCS. HostCore already models a plan, and the module engine gives
a module no database, so the equivalents live elsewhere:

- **Plans** are HostCore plans; their fields arrive as `service_config`.
- **IP allocation** is delegated to Proxmox and the network: a guest takes DHCP
  by default, or the static `ip_address`/`gateway` the plan sets. There is no
  module-side pool to exhaust.
- **The guest map** is the `server_id` HostCore persists from the deploy, and
  the node behind it is resolved per call rather than stored.

The WHMCS module's noVNC console and power buttons are client-area features of
WHMCS itself, not provisioning calls, and have no counterpart in the service
module surface.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/proxmox modules/proxmox/proxmox.wasm
make -C ../.. test      # runs the mock-Proxmox verification harness
```
