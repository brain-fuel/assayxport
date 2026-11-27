# The Haskell extractor's specification (LawSpec 0.32.0)

The extractor's logic is written here, in LawSpec, and generated as typed Go into the parent package `law`. `../../haskell.gp` holds only what LawSpec cannot do: walk the tree-sitter tree, read files, and wire the command line.

Everything below runs from this directory, in a checkout of the repository, with nothing outside it but the tools: LawSpec 0.32.0 (`lawspec`), Go, Python 3 and, for the conformance unit, an `ax` binary built from this checkout.

## What is here

| Path | What | In the repository |
|---|---|---|
| `lawspec.edn`, `lawspec.lock.edn` | The project. The contract is a local package (`contract/`), the Go test root is `lawtests/`, the binary under test is `bin/ax` | yes |
| `laws/` | Six units, 205 laws | yes |
| `contract/` | The package `assayxport.contract` 0.3.0 | yes |
| `lawtests/go.mod`, `go.sum` | The test root's module, `goforge.dev/assayxport/internal/extract/haskell/law/spec/lawtests` (a module of its own, so that the repository's `go build ./...` does not see the generated tests). Its `replace` of gotreesitter is the same pin as the repository's `go.mod` | yes |
| `lawtests/native/workspace/workspace.go` | The handler of the contract's `Workspace` ability | yes |
| `lawtests/assayxport/haskell/adapter.go` | The conformance adapter: runs `ax` (`AX_BIN`, by default `bin/ax`) | yes |
| `lawtests/assayxport/haskell/extract/adapter.go`, `lawtests/assayxport/haskell/policy/adapter.go`, `lawtests/lawspec/host/adapter.go` | The parser's handler and the two other hand-written adapters | yes |
| everything else under `lawtests/` | Written by `lawspec generate` (about 25 MB) | **no** (`.gitignore`) |
| `bin/ax` | The binary under test | **no** (`.gitignore`) |
| `tools/independence.sh`, `independence.py` | Rule I1: nothing `ax` runs reaches a function of a reference | yes |
| `tools/readme-check.sh` | The sentences of the top-level README that the laws stand behind | yes |

## The commands

```sh
cd internal/extract/haskell/law/spec

# 1. The lock is current, with no network.
lawspec fetch --locked --offline

# 2. The specification type-checks and its definitions are admitted (about 2 to 6 hours).
lawspec check --no-advice

# 3. Generate the test root (about 3.5 hours).
lawspec generate --no-advice

# 4. The package `law` in the repository is what the sources generate: every file, and no other.
gen=lawtests/assayxport/haskell/extract
for f in "$gen"/*.go; do
  case "$f" in *_test.go|*/adapter.go|*/lawspec_bound_handlers.go) continue;; esac
  cmp "$f" "../$(basename "$f")" || echo "law/$(basename "$f") is not what the specification generates"
done
for f in ../*.go; do
  case "$(basename "$f")" in *_test.go|adapter.go|lawspec_bound_handlers.go) echo "law/$(basename "$f") does not belong in the package"; continue;; esac
  [ -f "$gen/$(basename "$f")" ] || echo "law/$(basename "$f") is not a file the specification generates"
done

# 5. Rule I1, and the README's sentences.
tools/independence.sh
tools/readme-check.sh ../../../../../README.md ../../../../schema/schema.gp laws/conditions_policy.lawspec

# 6. The pure laws (no binary needed).
(cd lawtests && go test ./assayxport/haskell/extract/ ./assayxport/haskell/policy/ ./assayxport/haskell/cpp/ ./assayxport/conditions/...)

# 7. The conformance laws, on a binary built from this checkout.
(cd ../../../../.. && go build -o internal/extract/haskell/law/spec/bin/ax ./cmd/ax)
(cd lawtests && go test ./assayxport/haskell/ ./assayxport/contract/)
```

Step 6 is a plain `go test`: no `-timeout`, no `-skip`, no `-parallel`. On a 16-core machine the whole command takes about 345 s on a 16-core machine (the extract unit about 320 s, HSC-15 alone about 75 s; the policy, cpp and conditions units about a minute together); Go's default limit for a package is ten minutes. Step 7 takes about 31 minutes (1,850 s measured); it is longer than ten minutes, and is the one command that needs a flag: `go test -timeout 60m`.

To regenerate the package `law` after a change to `laws/`: steps 2 and 3, then copy the files step 4 compares, then `go tool goplus gen ./internal/extract/haskell/` at the repository root.

## What the laws hold, and what they do not

The laws hold `ax` to one rule above the others: **a fact is printed as certain only where the rules say ax decides; everywhere else it is kept with a condition that nothing decides, and named in a warning.** The preprocessor is followed exactly where its rule is simple (which lines a build keeps; plain macros replaced in code; C comments; lines joined at a backslash; whether one of the preprocessor's own names is defined; the CPP pragma in the file's header), and a build is marked where it is not (a macro with an argument list that is called, or may be; the preprocessor's own names in a line of code or read as a value; a name your `--when` values define, read in code; a header that cannot be read as GHC reads it).

No function that `ax` runs reaches a function of the references the laws judge it by (`assayxport.haskell.cpp`, `assayxport.haskell.policy`, and the evaluator of `assayxport.conditions.policy`), through any chain of calls: `tools/independence.sh` reads the generated package and fails where one does. The names the two sides do share (constants, the wording of warnings and of the decisions file, an empty environment and the two states of a name) are listed in `tools/independence.py`; what a binding line means is not among them.

**The package `law` also holds the references.** LawSpec generates one Go package for a unit, with every function the unit's laws use; the laws that judge the extraction are laws of the extraction's unit, so the reference preprocessor (`ran`), the generator of the fourth model (`lace`) and the reference evaluator are generated into `law` beside the code `ax` runs, and are linked into the binary (about 355 functions). `ax` calls none of them: that is what `tools/independence.sh` checks. They cannot be left out by a build constraint, since the generator writes both kinds into the same files.

What ax is not told it does not know: definitions in included files are not read, and a name that only the build defines (`-D`) and that is in none of your `--when` values is read as written.
