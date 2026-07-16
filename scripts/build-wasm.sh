#!/usr/bin/env bash
# Builds the browser-side WASM data engine (cmd/axwasm) and stages it,
# gzip-compressed, next to the //go:embed directives in internal/explorer.
# Both artifacts are committed (this is a go-install target, so they must be
# in the module); rebuild them whenever cmd/axwasm or the Go toolchain changes:
#
#     go generate ./internal/explorer/
#
# wasm_exec.js's location moved between Go releases (lib/wasm/ on current
# toolchains, misc/wasm/ on older ones); this checks both and fails loudly if
# neither has it rather than silently shipping a stale copy.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dist="$repo_root/internal/explorer/dist"
mkdir -p "$dist"

echo "building explorer.wasm (js/wasm)..." >&2
GOOS=js GOARCH=wasm go build -o "$dist/explorer.wasm" "$repo_root/cmd/axwasm"

goroot="$(go env GOROOT)"
if [ -f "$goroot/lib/wasm/wasm_exec.js" ]; then
	cp "$goroot/lib/wasm/wasm_exec.js" "$dist/wasm_exec.js"
elif [ -f "$goroot/misc/wasm/wasm_exec.js" ]; then
	cp "$goroot/misc/wasm/wasm_exec.js" "$dist/wasm_exec.js"
else
	echo "wasm_exec.js not found under $goroot/lib/wasm or $goroot/misc/wasm" >&2
	exit 1
fi

echo "compressing..." >&2
gzip -9 -f "$dist/explorer.wasm"
gzip -9 -f "$dist/wasm_exec.js"

echo "wrote $dist/explorer.wasm.gz and $dist/wasm_exec.js.gz" >&2
