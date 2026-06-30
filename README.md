# assayxport

> Assaying analyzes a metal to report its exact composition. `assayxport`
> analyzes a codebase to report its exact API composition - where each symbol
> lives, what package it belongs to, its signature, its docs, and whether it is
> a runnable entrypoint - as a deterministic JSON manifest at the project root,
> so an LLM, docgen, or tool reads one map instead of reparsing everything.

Part of the [goforge](https://goforge.dev) suite. SP1 supports Go; Python and
Java follow.

## Install

```bash
go install goforge.dev/assayxport/cmd/assayxport@latest
```

## Use

```bash
assayxport scan .            # writes assayxport.json + .assayxport/ shards
assayxport scan ./pkg --stdout   # print combined JSON, write nothing
```

## Output

- `assayxport.json` - root index: project metadata + one entry per package.
- `.assayxport/<package-dir>.json` - per-package shard with the full symbol list.

Output is deterministic: relative paths, no timestamps, stable ordering. Equal
inputs produce byte-identical files.

## License

MIT.
