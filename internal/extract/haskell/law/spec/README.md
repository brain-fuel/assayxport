# The Haskell extractor's specification (LawSpec 0.32.0)

The extractor's logic is written here, in LawSpec, and generated as typed Go
into the parent package `law` (`lawspec generate`). `../../haskell.gp` holds
only what LawSpec cannot do: walk the tree-sitter tree, read files, and wire
the CLI.

- `laws/`: six units, 160 laws.
  - `haskell_extract`, `haskell_policy`, `conditions`, `conditions_policy` and
    `haskell_cpp` are pure: they state the extraction, the `when` conditions
    and the CPP build walk, and check them against independent references
    (for example every assignment of the CPP names, `cpp.everyCopies`).
  - `haskell` is conformance: it runs the built `ax` binary (`AX_BIN`) on
    generated trees and checks the manifest, `--when`, the decisions file and
    `ax when init`.
- `contract/`: the shared `assayxport.contract` package the units depend on.
- `conformance/adapter.go.txt`: the native adapter the conformance unit's
  generated tests use (`Workspace`, `AssayWith`, ...). It lives in the test
  root named by `lawspec.edn`'s `:root`, which is outside this repository;
  where it should live here is an open question in the PR.

`lawspec check --no-advice` takes about 2 h and `generate --no-advice` about
3.4 h on a 16-core machine.
