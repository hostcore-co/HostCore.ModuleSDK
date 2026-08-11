# Virtualizor module

A reference HostCore service module for the [Softaculous Virtualizor](https://www.virtualizor.com)
VPS control panel. It provisions and manages virtual servers through the
[Virtualizor Admin API](https://www.virtualizor.com/docs/admin-api/).

> This module was built from Virtualizor's public Admin API documentation. Field
> and action (`act`) names follow that API so an operator can verify them
> against the panel.

## API shape

The Admin API is a single endpoint (`index.php`) selected by an `act` query
parameter, authenticated with an API **key** and **password** and returning JSON
when `api=json`. A VPS is addressed by the integer **vpsid** the panel allocates
at create time; HostCore persists the id `DeployService` returns and replays it
as `server_id` on later lifecycle calls.

## Exported functions → Admin API

| Function | Admin API `act` |
| --- | --- |
| `TestModuleConnection` | `servers` |
| `DeployService` | `addvs` |
| `SuspendService` | `vs&suspend=<vpsid>` |
| `UnsuspendService` | `vs&unsuspend=<vpsid>` |
| `TerminateService` | `vs&delete=<vpsid>` |
| `UpdateService` | `editvs&vpsid=<vpsid>&plid=<plan>` |
| `ChangePassword` | `editvs&vpsid=<vpsid>&rootpass=<secret>` |

## Input

| Field | Purpose |
| --- | --- |
| `domain` | Virtualizor host (no scheme). |
| `api_key` | Virtualizor Admin API key. |
| `api_pass` | Virtualizor Admin API password (top-level, or the `api_pass` service_config value). |
| `server_id` | vpsid, supplied on lifecycle calls after deploy. |
| `password` | VPS root password (deploy / change-password). Generated on deploy if blank. |
| `user` | `{ id, email, first_name, last_name }`. |

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `plan_id` | number | **Required.** Virtualizor plan (`plid`). |
| `os_id` | number | **Required.** OS template (`osid`). |
| `virt` | string | Virtualization type. Defaults to `kvm`. |
| `node_id` | number | Node (`serid`) to deploy on. Auto-selected when blank. |
| `ips` | number | IPv4 addresses to assign. Defaults to 1. |
| `hostname` | string | VPS hostname. Derived from the domain when blank. |
| `space`, `ram`, `bandwidth`, `cores` | number | Optional plan overrides. |
| `port` | number | Admin API port. Defaults to 4085. |

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/virtualizor modules/virtualizor/virtualizor.wasm
make -C ../.. test      # runs the mock-Virtualizor verification harness
```
