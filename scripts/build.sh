#!/usr/bin/env bash
#
# Build a HostCore module script to a WASM reactor module.
#
# By default it builds the example Pterodactyl module. Pass a package path and
# output file to build another module:
#
#   scripts/build.sh ./modules pterodactyl.wasm
#
# Two tool-chains are supported. TinyGo is preferred because it produces much
# smaller binaries; the standard Go wasip1 tool-chain is used as a fallback and
# is fully compatible (both honour //go:wasmexport / //go:wasmimport).
set -euo pipefail

PKG="${1:-./modules}"
OUT="${2:-modules/pterodactyl.wasm}"

cd "$(dirname "$0")/.."

if command -v tinygo >/dev/null 2>&1; then
	echo "› building with TinyGo → $OUT"
	tinygo build -target=wasip1 -buildmode=c-shared -no-debug -o "$OUT" "$PKG"
else
	echo "› TinyGo not found, building with Go wasip1 → $OUT"
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o "$OUT" "$PKG"
fi

echo "› built $(du -h "$OUT" | cut -f1) $OUT"
