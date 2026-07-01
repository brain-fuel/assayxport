# assayxport

> Assaying analyzes a metal to report its exact composition. `assayxport`
> analyzes a codebase to report its exact API composition - where each symbol
> lives, what package it belongs to, its signature, its docs, and whether it is
> a runnable entrypoint - as a deterministic JSON manifest at the project root,
> so an LLM, docgen, or tool reads one map instead of reparsing everything.

Part of the [goforge](https://goforge.dev) suite. Supports Go (native
`go/packages`) and Python (a pure-Go, cgo-free tree-sitter parser); Java
follows. One scan produces a single deterministic manifest covering every
supported language found in the tree.

## Install

```bash
go install goforge.dev/assayxport/cmd/assayxport@latest
```

## Use

```bash
assayxport scan .            # writes assayxport.json + .assayxport/ shards
assayxport scan ./pkg --stdout   # print combined JSON, write nothing
assayxport scan . --lang python  # restrict to one language (repeatable)
```

### Languages

By default `scan` runs every registered extractor and merges the results into
one manifest whose `languages` field lists what was found. Use `--lang` to
restrict the run; it is repeatable, so `--lang python --lang go` runs only
those two. An unregistered language name is an error that lists the available
ones.

Go extraction is fully semantic via `go/packages`. Python extraction is
syntactic (tree-sitter): names, kinds, visibility, signatures as written,
docstrings, decorators, and entrypoints are reliable; imported-name resolution
and inferred types are not.

## Output

- `assayxport.json` - root index: project metadata + one entry per package.
- `.assayxport/<package-dir>.json` - per-package shard with the full symbol list.

Output is deterministic: relative paths, no timestamps, stable ordering. Equal
inputs produce byte-identical files.

## License

MIT. Third-party components (the tree-sitter runtime and the Python grammar,
both MIT) are attributed in [NOTICE](NOTICE).
