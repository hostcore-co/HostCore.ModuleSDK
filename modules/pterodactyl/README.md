# Pterodactyl module

A reference HostCore service module that provisions and manages game /
application servers on a [Pterodactyl](https://pterodactyl.io) panel.

It is modelled on the [PteroSync](https://github.com/wohahobg/PteroSync) WHMCS
module and goes well beyond naive server creation. Its headline capability is
**intelligent multi-port allocation**: it assigns every port a game needs
(`SERVER_PORT`, `QUERY_PORT`, `RCON_PORT`, …) from configurable ranges,
guarantees they all land on a **single node IP**, and wires the chosen ports
into the server's start-up environment automatically.

## Exported functions

| Function | Trigger | Behaviour |
| --- | --- | --- |
| `TestModuleConnection` | binding test | Verifies panel reachability, key validity and that at least one node exists. |
| `GetServiceConfigOptions` | UI render | Returns the configurable-field schema (below). |
| `DeployService` | `service.deploy` | Creates the panel user (if needed), the server, allocates all ports on one IP and injects them into the environment. |
| `SuspendService` | `service.suspend` | Suspends the server. |
| `UnsuspendService` | `service.unsuspend` | Restores a suspended server. |
| `UpdateService` | `service.update` | Re-applies resource limits, feature limits, egg, image, start-up and environment. |
| `TerminateService` | `service.terminate` | Permanently deletes the server. |
| `ChangePassword` | password change | Updates the panel user's password. |

## Input

Top-level argument fields provided by the Engine:

| Field | Purpose |
| --- | --- |
| `domain` | Panel host (no scheme), e.g. `panel.example.com`. |
| `api_key` | Pterodactyl **application** API key (`ptla_…`). |
| `service_id` | Store service id, stored on the panel as `external_id`. |
| `password` | Desired panel password (deploy / change-password). Optional. |
| `user` | `{ id, email, first_name, last_name }` for the owning client. |

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `nest_id`, `egg_id`, `location_id` | number | **Required.** What to deploy and where. |
| `server_name` | string | Panel display name. Defaults to `hostcore_<service_id>`. |
| `cpu` | number | CPU limit %. `0` = unlimited. |
| `memory` / `memory_unit` | number / `MB`\|`GB` | Memory limit (unit defaults to MB). |
| `disk` / `disk_unit` | number / `MB`\|`GB` | Disk limit. |
| `swap` / `swap_unit` | number / `MB`\|`GB` | Swap limit. |
| `io` | number | Block IO weight (10–1000). |
| `threads` | string | CPU pinning, e.g. `0-1,3`. Blank = all cores. |
| `databases`, `backups`, `allocations` | number | Client feature limits. |
| `ports_ranges` | JSON | **Required.** Named port ranges — see below. |
| `default_variables` | JSON | Egg variable overrides, e.g. `{"MAX_PLAYERS":"30"}`. |
| `server_port_offset` | number | Reserve an extra allocation at `SERVER_PORT + offset` (e.g. `1` for ARK). |
| `dedicated_ip` | boolean | Only deploy onto an IP with no other assigned allocations. |
| `dedicated_port` | boolean | Always assign the first port of the `SERVER_PORT` range (fixed port). |
| `image`, `startup` | string | Override the egg's Docker image / start-up command. |
| `oom_disabled` | boolean | Disable the OOM killer. |
| `feature_limits` | JSON | Extra feature-limit overrides merged into the server. |

### Port ranges

`ports_ranges` is a JSON object mapping an **environment variable name** to a
port range. `SERVER_PORT` is required and becomes the server's primary
allocation; every other key must match an egg variable and is attached as an
additional allocation and exported as that environment variable.

```json
{
  "SERVER_PORT": "25565-25669",
  "QUERY_PORT":  "27015-27020",
  "RCON_PORT":   "27020-27030"
}
```

Two special keys are recognised:

* `EXTRA_ALLOCATION` — `"start-end"` or `"start-end:N"` reserves `N` nameless
  extra allocations (no environment variable).
* combined with `server_port_offset`, an extra allocation at
  `SERVER_PORT + offset` is reserved on the same IP.

All requested ports are placed on **one** IP; if no single IP can satisfy every
range the deploy fails cleanly rather than scattering ports across hosts.

## Deploy flow

1. Reject the deploy if a server already exists for this `service_id`.
2. Resolve the panel user: by `external_id`, else adopt an account with the same
   e-mail (re-linking `external_id`), else create one.
3. Load the egg (docker image, start-up command, variables) and build the
   environment from egg defaults overridden by `default_variables`.
4. Create the server, constraining the first allocation to the `SERVER_PORT`
   range (and `dedicated_ip` if requested).
5. If more than `SERVER_PORT` is required, read the node's allocations
   (paginated), find a co-located free-port set, `PATCH …/build` to attach them,
   and `PATCH …/startup` to inject the port environment variables.

## Output

`DeployService` / `UpdateService` return a data document the store can persist:

```json
{
  "server_id": 5001,
  "uuid": "…",
  "identifier": "1a2b3c",
  "user_id": 100,
  "ip": "1.2.3.4",
  "port": 25565,
  "address": "1.2.3.4:25565",
  "ports": { "SERVER_PORT": 25565, "QUERY_PORT": 27016 }
}
```

## Build & test

```sh
make build     # → pterodactyl.wasm
make test      # build + end-to-end verification harness (see ../test)
```
