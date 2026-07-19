# cPanel & WHM module

A reference HostCore service module for cPanel/WHM web hosting. It provisions
and manages hosting accounts through [WHM API 1](https://api.docs.cpanel.net/whm/introduction/) —
the same server-side surface a WHMCS integration drives.

Unlike the game-panel modules, an account is addressed by its **cPanel
username**. Requests are authenticated GETs to the WHM JSON API on the WHM SSL
port (2087 by default); parameters travel in the query string and the response
envelope reports success via `metadata.result`.

## Exported functions → WHM API

| Function | WHM API 1 call |
| --- | --- |
| `TestModuleConnection` | `version` |
| `DeployService` | `createacct` |
| `SuspendService` | `suspendacct` |
| `UnsuspendService` | `unsuspendacct` |
| `TerminateService` | `removeacct` |
| `UpdateService` | `changepackage` |
| `ChangePassword` | `passwd` |
| `GetServiceConfigOptions` | — |

## Input

| Field | Purpose |
| --- | --- |
| `domain` | WHM server host (no scheme). |
| `api_key` | WHM API token. |
| `username` | WHM API user the token belongs to. Defaults to `root`. |
| `service_id` | Store service id; part of the derived account username. |
| `password` | cPanel account password (deploy / change-password). A random one is generated on deploy if blank. |
| `user` | `{ id, email, first_name, last_name }`. |

Authentication uses the WHM token scheme: `Authorization: whm <username>:<token>`.

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `domain` | string | **Required.** The hosting account's primary domain. |
| `package` | string | **Required.** WHM package (hosting plan). |
| `account_username` | string | cPanel username override. Derived from the domain when blank. |
| `contactemail` | string | Account contact email. Defaults to the client's email. |
| `port` | number | WHM API SSL port. Defaults to 2087. |
| `dedicated_ip` | boolean | Assign a dedicated IP (`ip=y`). |

### Username derivation

When `account_username` is blank the module derives a cPanel-valid username from
the account domain (lower-case, alphanumeric, starting with a letter, ≤ 16
chars) plus digits from the service id for uniqueness. Suspend, unsuspend,
terminate, update and change-password all derive the same username, so they act
on the account deploy created.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/cpanel modules/cpanel/cpanel.wasm
make -C ../.. test      # runs the mock-WHM verification harness
```
