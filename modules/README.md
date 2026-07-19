# Example modules

Each subdirectory is a self-contained HostCore service module: a `package main`
Go program compiled to a WASM reactor, a prebuilt `.wasm`, and a README.

| Module | Application | Notes |
| --- | --- | --- |
| [`pterodactyl`](pterodactyl) | [Pterodactyl](https://pterodactyl.io) game panel | Client-side intelligent multi-port allocation (SERVER_PORT + additional ports on one IP). |
| [`wisp`](wisp) | [WISP](https://wisp.gg) game panel | Panel-side additional-port allocation; ULID-safe ids; server templates. |

All modules export the same lifecycle surface — `TestModuleConnection`,
`GetServiceConfigOptions`, `DeployService`, `SuspendService`,
`UnsuspendService`, `UpdateService`, `TerminateService`, `ChangePassword` — and
import only the [HostCore.ModuleSDK](..).

## Build

```sh
make build                                  # every module
scripts/build.sh ./modules/wisp modules/wisp/wisp.wasm   # just one
```

## Add a module

1. Create `modules/<name>/<name>.go` as `package main` with an empty `func main() {}`.
2. Export the lifecycle functions with `//go:wasmexport`.
3. `make build` compiles it to `modules/<name>/<name>.wasm`.
