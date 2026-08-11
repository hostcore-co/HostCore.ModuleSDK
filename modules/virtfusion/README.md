# VirtFusion module

A reference HostCore service module for the [VirtFusion](https://virtfusion.com)
VPS/KVM control panel. It provisions and manages virtual servers through the
VirtFusion API v1 — the same surface the VirtFusion WHMCS module drives.

## Server id round-trip

Unlike the game panels, VirtFusion has no external-id lookup for servers: the
panel allocates an integer **server id** at create time. `DeployService` returns
it, HostCore persists it on the service, and passes it back as `server_id` on
every later lifecycle call. Those calls fail cleanly if `server_id` is absent.

The store user's id is used as the VirtFusion `extRelationId`, linking the panel
user to the store client.

## Exported functions → VirtFusion API

| Function | VirtFusion API v1 |
| --- | --- |
| `TestModuleConnection` | `GET /packages` |
| `DeployService` | `GET /users/{ext}/byExtRelation` (or `POST /users`) then `POST /servers` |
| `SuspendService` | `POST /servers/{id}/suspend` |
| `UnsuspendService` | `POST /servers/{id}/unsuspend` |
| `TerminateService` | `DELETE /servers/{id}` |
| `UpdateService` | `PUT /servers/{id}/package/{package_id}` |
| `ChangePassword` | `POST /users/{ext}/byExtRelation/resetPassword` |

`ChangePassword` resets the panel password; VirtFusion **generates** the new
value and the module returns it in the result — the endpoint cannot set a chosen
password.

## Input

| Field | Purpose |
| --- | --- |
| `domain` | VirtFusion host (no scheme). |
| `api_key` | VirtFusion API token (sent as `Authorization: Bearer`). |
| `server_id` | Panel server id, supplied on lifecycle calls after deploy. |
| `user` | `{ id, email, first_name, last_name }`. The `id` is the `extRelationId`. |

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `hypervisor_id` | number | **Required.** Hypervisor group to deploy into. |
| `package_id` | number | **Required.** Package defining the server resources. |
| `ipv4` | number | IPv4 addresses to assign. Defaults to 1. |
| `storage`, `memory`, `traffic`, `cpu_cores` | number | Optional package overrides. |
| `network_speed_inbound`, `network_speed_outbound` | number | Optional network overrides. |
| `network_profile`, `storage_profile` | number | Optional profile ids. |

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/virtfusion modules/virtfusion/virtfusion.wasm
make -C ../.. test      # runs the mock-VirtFusion verification harness
```
