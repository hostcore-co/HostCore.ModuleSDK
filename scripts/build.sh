#!/usr/bin/env bash
#
# Build a HostCore module script to a WASM reactor module.
#
# With no arguments it builds every example module under modules/*. Pass a
# package path and output file to build a single module:
#
#   scripts/build.sh ./modules/wisp modules/wisp/wisp.wasm
#
# Two tool-chains are supported. TinyGo is preferred because it produces much
# smaller binaries; the standard Go wasip1 tool-chain is used as a fallback and
# is fully compatible (both honour //go:wasmexport / //go:wasmimport).
set -euo pipefail

cd "$(dirname "$0")/.."

build_one() {
	local pkg="$1" out="$2"
	if command -v tinygo >/dev/null 2>&1; then
		echo "› building with TinyGo → $out"
		tinygo build -target=wasip1 -buildmode=c-shared -no-debug -o "$out" "$pkg"
	else
		echo "› TinyGo not found, building with Go wasip1 → $out"
		GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o "$out" "$pkg"
	fi
	echo "› built $(du -h "$out" | cut -f1) $out"
}

if [ "$#" -ge 2 ]; then
	build_one "$1" "$2"
	exit 0
fi

for dir in modules/*/; do
	name="$(basename "$dir")"
	build_one "./$dir" "$dir$name.wasm"
done
