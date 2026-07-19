# WISP module

A reference HostCore service module for the [WISP](https://wisp.gg) game panel,
a Pterodactyl derivative. It is a sibling to the [Pterodactyl module](../pterodactyl)
and exports the same lifecycle, but speaks WISP's API and provisioning model.

## How it differs from the Pterodactyl module

| Concern | Pterodactyl module | WISP module |
| --- | --- | --- |
| Egg endpoint | `nests/<nest>/eggs/<egg>` | `eggs/<egg>` (flat) |
| User search | `users?filter[email]=` | `users?search=` |
| Resource ids | integers | integers **or** opaque strings (ULIDs) |
| Extra ports | resolved client-side onto one IP | resolved **by the panel** via `additional_ports` |
| Blank limits | sent as `0` | sent as `null` (inherit from server template) |
| Extras | dedicated port, port offset | force-outgoing-ip, node tags, server templates, packs |

WISP v2 ids may be opaque strings such as `user_01k9…`. The module never casts
ids to integers: numeric ids are emitted as JSON numbers and everything else as
JSON strings, so both panel generations work.

## Exported functions

`TestModuleConnection`, `GetServiceConfigOptions`, `DeployService`,
`SuspendService`, `UnsuspendService`, `UpdateService`, `TerminateService`,
`ChangePassword`.

## Input

| Field | Purpose |
| --- | --- |
| `domain` | Panel host (no scheme). |
| `api_key` | WISP application API key. |
| `service_id` | Store service id → panel `external_id`. |
| `password` | Desired panel password (deploy / change-password). Optional. |
| `user` | `{ id, email, first_name, last_name }`. |

Egg-variable overrides are read from `service_config` by the variable's own
environment key: a `MAX_PLAYERS` entry overrides the `MAX_PLAYERS` egg variable.

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `egg_id`, `location_id` | string | **Required.** Numeric or WISP string ids. |
| `server_name` | string | Panel display name. Defaults to `hostcore_<service_id>`. |
| `cpu`, `memory`, `disk`, `swap`, `io` | number | Limits in % / MB. **Blank inherits from the server template.** |
| `databases`, `allocations` | number | Client feature limits. |
| `backup_megabytes_limit`, `backup_count_limit` | number | Backup size / count limits. |
| `port_range` | string | Comma-separated ranges for the primary allocation. |
| `additional_ports` | JSON | Additional allocations — see below. |
| `additional_port_fail_mode` | string | `continue` or `stop`. |
| `tags` | string | Comma-separated node tag slugs. |
| `dedicated_ip`, `force_outgoing_ip`, `oom_disabled` | boolean | |
| `pack_id` | string | Pack to install with (optional). |
| `image`, `startup` | string | Override egg image / start-up (optional). |
| `server_template_id` | string | Server template this product maps to (optional). |

### Additional ports

`additional_ports` is a JSON object keyed by offset-from-primary. Each value is
an egg variable name, a variable with a range, or `NONE` for a reserve-only
port. The module translates it into WISP's additional-port specification and the
panel binds the allocations server-side.

```json
{
  "1": "QUERY_PORT",
  "2": "RCON_PORT:27015-27020",
  "3": "NONE"
}
```

* `"QUERY_PORT"` → offset entry: the port at primary+1 bound to `QUERY_PORT`.
* `"RCON_PORT:27015-27020"` → range entry bound to `RCON_PORT`.
* `"NONE"` → a reserved port with no variable binding.

`additional_port_fail_mode` decides whether the deploy continues or fails when
the panel cannot satisfy the additional ports.

## Update ordering

`UpdateService` applies changes in a deliberate order: the egg/start-up first (a
package change may swap the egg), then the server-template relink (so blank
limits can inherit from it), then the build limits last.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/wisp modules/wisp/wisp.wasm
make -C ../.. test      # runs the mock-panel verification harness
```
