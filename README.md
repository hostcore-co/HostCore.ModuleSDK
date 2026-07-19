# HostCore.ModuleSDK

The SDK for writing **HostCore service modules** — small, sandboxed WebAssembly
programs that let a store integrate with an external application (a Pterodactyl
panel, cPanel, an email or domain provider, …).

A module is a Go (TinyGo-compatible) program compiled to a WASM *reactor*. The
[HostCore Module Engine](https://github.com/hostcore-co/HostCore.ModuleEngine)
loads it and calls its exported functions in response to store events. For
security a module may import **only this SDK** — no other third-party packages —
and all outbound network access is mediated by the Engine's host functions.

```
store event ──▶ Module Engine ──▶ WASM module (your code) ──▶ SDK host calls ──▶ external API
                                        ▲                                             │
                                        └─────────────── JSON args / result ──────────┘
```

## What the SDK gives you

| Area | Helpers |
| --- | --- |
| Input | `GetArgs`, `Args.User`, `Args.ServiceConfig`, `Args.Get`, `Args.Raw` |
| Output | `Success`, `Fail` |
| HTTP (host-mediated) | `HTTPGet/Post/Put/Patch/Delete` → `HTTPResponse` |
| JSON reading | `Result` (typed accessors, `Get`, `ForEach`, `Array`), `Parse`, `ParseBytes`, `GetBytes` |
| JSON writing | `NewJSON().Set(...).Build()`, `JSONInt64Array`, `JSONStringArray`, `JsonString` |
| Conversions | `Itoa`, `Itoa64`, `Atoi`, `Atoi64`, `ParseFloat`, `ParseBool`, `GBToMB` |
| Misc | `RandomString`, `TrimSpace`, `ParsePortRange` |

Everything is allocation-light and avoids `encoding/json`/`reflect` so modules
stay small and fast.

## Anatomy of a module

```go
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

func main() {} // required; never runs for exported calls

//go:wasmexport DeployService
func DeployService() int32 {
    args := sdk.GetArgs()
    user := args.User()
    region := args.ServiceConfig("region")

    resp := sdk.HTTPPost("https://api.example.com/servers",
        map[string]string{"Authorization": "Bearer " + args.Get("api_key").String()},
        sdk.NewJSON().Set("owner", user.Email).Set("region", region).Build())

    if !resp.OK {
        return sdk.Fail("provision failed", resp.Body)
    }
    return sdk.Success(sdk.NewJSON().Set("id", sdk.GetBytes(resp.Body, "id").String()).Build())
}
```

### Input contract

The Engine passes each call a JSON document. Well-known top-level fields are
exposed through typed accessors (`Args.User()`, `Args.Get("domain")`,
`Args.Get("api_key")`, `Args.Get("service_id")`); per-service configuration
values advertised by `GetServiceConfigOptions` arrive in a `service_config`
key/value array and are read with `Args.ServiceConfig("key")`.

### Output contract

Return through `sdk.Success(data)` (exit 0) or `sdk.Fail(message, data)`
(exit 1). Both accept raw JSON bytes (or `nil`). The Engine wraps this into
`{"success":…,"error":…,"data":…}`.

## Building

```sh
make build          # → modules/pterodactyl.wasm
```

`scripts/build.sh` prefers **TinyGo** (smallest output) and transparently falls
back to the standard **Go wasip1** tool-chain (`GOOS=wasip1 GOARCH=wasm go build
-buildmode=c-shared`). Both produce a reactor module exporting the same
functions and importing the same `http_*` host functions.

## Testing

```sh
make test           # builds the module, then runs the verification harness
```

The harness in [`test/`](test) is a self-contained Go module (kept out of the
SDK's dependency graph on purpose). It loads the compiled WASM in a real
[extism](https://extism.org) runtime with the **same** host-function wire format
as the Engine, but routes HTTP to an in-memory mock Pterodactyl panel. It drives
deploy / suspend / update / terminate end-to-end and asserts on the intelligent
multi-port allocation behaviour.

## Example module

[`modules/pterodactyl.go`](modules/pterodactyl.go) is a production-grade
reference module that manages game/application servers on a Pterodactyl panel.
See [`modules/README.md`](modules/README.md) for its full contract, configuration
options and provisioning flow.
