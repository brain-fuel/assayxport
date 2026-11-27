#!/bin/bash
# S1.j next cycle 9 (review 11, S1; design.md, section 6, rule I1): nothing
# ax runs may reach a function of a reference through any chain of calls.
#   independence.sh [AX_ROOT]     AX_ROOT: the repository (default: the one this script is in)
# Reads the generated package internal/extract/haskell/law and the calls
# ax's own sources make into it. Exit 1 with each chain when a reference
# is reached; see independence.py for what a reference is.
here=$(cd "$(dirname "$0")" && pwd)
root=${1:-$(cd "$here" && git rev-parse --show-toplevel 2>/dev/null)}
law=$root/internal/extract/haskell/law
[ -f "$law/lawspec_typed.go" ] || { echo "independence: no generated package at $law"; exit 2; }
exec python3 -I "$here/independence.py" --ax "$root/cmd" --ax "$root/internal" "$law/lawspec_typed.go" "$law/lawspec_fast.go" "$law/lawspec_definitions.go"
