# Plesk module

A reference HostCore service module for Plesk (Obsidian) web hosting. It
provisions and manages subscriptions through the
[Plesk REST API v2](https://docs.plesk.com/en-US/obsidian/api-rpc/about-rest-api.79359/).

The legacy Plesk WHMCS integration drives the XML-RPC packet API. This module
uses the modern JSON REST API and its **CLI gateway**
(`POST /api/v2/cli/<utility>/call`), which runs Plesk's command-line utilities
server-side and returns JSON — a clean fit for the SDK's JSON tooling. A CLI
call succeeds when its response `code` is `0`; otherwise `stderr` carries the
reason.

## Exported functions → Plesk CLI

| Function | Plesk CLI call |
| --- | --- |
| `TestModuleConnection` | `GET /api/v2/server` |
| `DeployService` | `customer --create` (if needed) then `subscription --create` |
| `SuspendService` | `subscription --suspend` |
| `UnsuspendService` | `subscription --activate` |
| `TerminateService` | `subscription --remove` |
| `UpdateService` | `subscription --switch-subscription -service-plan` |
| `ChangePassword` | `subscription --update -login -passwd` |

## Input

| Field | Purpose |
| --- | --- |
| `domain` | Plesk server host (no scheme). |
| `api_key` | Plesk REST API key (sent as `X-API-Key`). |
| `service_id` | Store service id; part of the derived customer login. |
| `password` | Subscription system-user password (a random one is generated on deploy if blank). |
| `user` | `{ id, email, first_name, last_name }`. |

## Configuration options (`service_config`)

| Key | Type | Notes |
| --- | --- | --- |
| `domain` | string | **Required.** The subscription's primary domain. |
| `plan` | string | **Required.** Plesk service plan name. |
| `username` | string | System (FTP) login. Derived from the domain when blank. |
| `ip` | string | IP address to assign. Plesk auto-selects when blank. |
| `port` | number | Plesk REST API port. Defaults to 8443. |
| `customer_login` | string | Owning customer login. Derived from the client when blank. |

## Deploy flow

1. Create the owning customer via `customer --create` when `customer --info`
   shows it does not exist.
2. Create the subscription via `subscription --create <domain> -owner <customer>
   -service-plan <plan> -login <sysuser> -passwd <secret>` (with `-ip` when set).

Suspend, unsuspend, switch-plan, change-password and terminate all address the
subscription by its domain, so they act on what deploy created.

## Build & test

```sh
make -C ../.. build     # or: scripts/build.sh ./modules/plesk modules/plesk/plesk.wasm
make -C ../.. test      # runs the mock-Plesk verification harness
```
