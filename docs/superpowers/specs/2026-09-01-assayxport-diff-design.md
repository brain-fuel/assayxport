# assayxport `ax diff` - design spec

> Scan two sources - any mix of local directories, local git refs, and remote
> git repositories - and report how they relate: exact identity-based drift
> for sources that share history, or ranked similarity candidates for
> unrelated codebases. Foundation for cross-codebase duplicate discovery.

- **Date:** 2026-09-01
- **Status:** approved, implemented (amendments from implementation noted inline)
- **Builds on:** SP1-SP4 (schema, emit, registry, extractors), the Node/TS
  extractor, and the `ExtractionOutcome` sum in `internal/extract`.
- **Scope:** source-spec grammar + resolver, `--mode drift`, `--mode
  correspond`, `assayxport-diff.json` + text rendering. No clustering, no
  extraction/codegen, no embeddings, no vendored git.

One stale assumption in the original brief: a TypeScript/JavaScript extractor
**does** exist now (`internal/extract/typescript`, registered in
`registry.All()`). `ax diff` inherits it through the registry for free; nothing
JS-specific is designed here.

## Architecture

```
internal/source           spec grammar + resolver: parse, git shell-out, cache
internal/diff             diff schema (wire types), drift matching, correspond
                          scoring, shared type-kind normalization, text render
cmd/ax                    runDiffCmd wiring (diff.gp), exit-code handling
```

- **`internal/source`** is plain Go, like `internal/artifact/maven` - it is
  process/IO plumbing (exec `git`, tar extraction, cache dirs), not semantics.
- **`internal/diff`** is Go+ authored (`.gp` + committed `_gp.go`), like
  `schema`/`registry` - change kinds, the type-kind vocabulary, and diff mode
  are natural exhaustive sums, and scoring is pure and law-testable
  (symmetry, determinism, score range are `rapid` properties).
- Extraction is **reused, not rebuilt**: each resolved source goes through
  `registry.Select` + `registry.RunOutcome` + `emit.Manifest`, producing the
  same in-memory `schema.Index` + shards `ax assay --stdout` would. `ax diff`
  never writes manifests into either source tree.

## Part 1 - source specs and resolution

### Grammar

```
spec      := source [ "#" ref ]
source    := local-path | remote-url
remote-url:= scheme "://" ...            (https, http, ssh, git)
           | user "@" host ":" path     (scp-style)
ref       := any git rev-parse-able name (branch, tag, SHA, HEAD~2, ...)
```

Classification is purely syntactic (no filesystem or network probe at parse
time, so parse tests are hermetic): a spec whose source part matches a URL
scheme or scp-style `user@host:path` is remote; everything else is a local
path. The ref separator is the **last** `#` in the spec; there is no escape
mechanism, so a local directory whose own name contains `#` cannot carry a
ref (documented limitation - the bare path still works because a path with no
recognizable trailing ref that exists as-typed is only rejected at resolve
time, see below).

Worked examples of every form:

| Spec | Meaning |
|---|---|
| `.` | working directory, scanned in place |
| `./pkg`, `/abs/path` | local directory, scanned in place |
| `./repo#v1.2.0` | local repo, tag `v1.2.0`, materialized via `git archive` |
| `./repo#main`, `.#HEAD~3` | local repo at branch / relative rev |
| `../fork#a1b2c3d` | local repo at a commit SHA |
| `https://github.com/owner/repo` | remote, default branch |
| `https://gitlab.example.com/g/sub/repo#develop` | self-hosted remote at branch |
| `git@github.com:owner/repo.git#v2.0.0` | scp-style remote at tag |
| `git@bitbucket.org:owner/repo` | scp-style remote, default branch |

`@` is never a ref separator (scp-style URLs own it); `#` is unambiguous
against both paths-in-practice and URLs (a URL fragment has no meaning to
git). Parse errors are specific: empty source, empty ref after `#`, a
`#ref` on something that resolves to a plain non-repo directory.

### Resolution

`source.Resolve(ctx, spec, Options) (Resolved, error)` where

```go
type Resolved struct {
    Dir    string // local directory ready to scan
    Label  string // stable display label (see below)
    Kind   string // "dir" | "local-git" | "remote-git"
    Commit string // full SHA; "" for Kind "dir"
    Ref    string // ref as given; "" if none
    Cleanup func() // removes temp materialization; no-op for dir/cache
}
```

- **Local dir, no ref**: used in place, never mutated. If it happens to be a
  git repo, the *working tree* is scanned - that is the point of the form.
- **Local repo + ref**: `git -C <path> rev-parse --verify <ref>^{commit}`
  resolves the SHA, then `git -C <path> archive --format=tar <sha>` is
  extracted **in-process with `archive/tar`** into a scratch temp dir (no
  system `tar` dependency, works on Windows). No worktree, no checkout, no
  touch of HEAD/index/stash; `Cleanup` removes the temp dir.
- **Remote + optional ref**:
  1. Resolve without cloning: `git ls-remote --symref <url> HEAD` for the
     default branch, `git ls-remote <url> <ref>` (trying `refs/tags/<ref>^{}`,
     `refs/tags/<ref>`, `refs/heads/<ref>`, then literal) for a named ref. A
     full 40-hex ref skips ls-remote entirely.
  2. Cache key: `<cache>/assayxport/git/<sha256(canonical-url)[:16]>/<sha>/`.
     A resolved SHA is immutable, so a cache hit is final - no network.
  3. Miss: `git init` + `git fetch --depth 1 <url> <sha>` into a temp dir,
     checkout `FETCH_HEAD`, strip `.git`, atomically rename the bare tree
     into the cache slot. Servers that refuse fetch-by-SHA (rare; GitHub /
     GitLab / Bitbucket all allow it) get a fallback `fetch --depth 1 <url>
     <refname>` whose head must equal the resolved SHA, else error.
  4. A small `meta.json` beside each tree records url, ref, sha, so `--offline`
     can map a previously seen `(url, ref)` to its cached sha. `--offline`
     with an unseen branch ref is an error naming the missing resolution;
     with a full SHA it just requires the cache slot to exist.
- All git invocations run with `GIT_TERMINAL_PROMPT=0` and
  `GIT_CONFIG_PARAMETERS` untouched, inheriting credential helpers, ssh
  agent, `insteadOf`, proxies, and CA config. A missing `git` binary fails
  up front: `ax diff requires the git binary on PATH to resolve "<spec>"`.
  Auth/network failures surface git's stderr trimmed to the useful line,
  prefixed with the host: `github.com: authentication failed (...)`.

Canonical URL (identity for cache sharing and drift auto-detection, never for
fetching): lowercase host, path with a single trailing `.git` stripped,
scp-style rewritten to `host/path`. So `https://github.com/o/r`,
`git@github.com:o/r.git`, and `ssh://git@github.com/o/r` share one identity.

### Labels

The default label must satisfy the golden requirement "two specs naming the
same commit produce identical output", so it cannot echo the spec as typed:

- git source (local or remote): `<canonical-name>#<sha12>` where
  canonical-name is `host/path` for remotes and the cleaned relative path as
  typed (`./repo` -> `repo`) for local repos.
- plain dir: the path as typed, slash-cleaned. (The user typed it; a relative
  path is not "a filesystem location" leak. An absolute path given by the
  user appears as its final element only, keeping output machine-portable.)
- `--label-a` / `--label-b` override unconditionally.

Note the asymmetry this accepts: `./repo#main` and
`https://github.com/o/repo#main` naming the same commit still differ in label
(`repo#sha12` vs `github.com/o/repo#sha12`). The golden test for
"same commit, two spec forms" therefore uses a branch name vs the SHA against
one remote, which is also the realistic reproducibility case.

### Go extraction on foreign sources - failure policy (decision)

**Policy: attempt the semantic load; on any load error, abort the whole diff
with a specific error. Never degrade silently, never emit a partial Go
manifest.** There is no syntactic Go extractor to degrade to today, and
building one (a Go tree-sitter grammar through `internal/ts`) is its own
sub-project; pretending `--mode correspond` results are comparable when one
side lost its Go symbols would poison exactly the ranking this tool exists
to produce.

Mechanics:

- `ax diff` consumes `registry.RunOutcome` and treats `ExtractionPartial` as
  **fatal** (unlike `ax assay`, which tolerates it with warnings - that
  tolerance is the "confidently wrong diff" hazard named in the brief). The
  error names the language, the source label, and the underlying loader
  message (module resolution errors come through verbatim from
  `go/packages`).
- The escape hatch is explicit, not automatic: `--lang` (repeatable, same
  registry aliases as `assay`) restricts extraction, so `--lang python
  --lang java` diffs a repo whose Go half cannot load. The restriction is
  recorded in the output header, so the narrowing is visible in the artifact.
- Go loads run with the ambient toolchain; `--offline` additionally sets
  `GOPROXY=off GOFLAGS=-mod=mod` on the child `go` process so no network
  sneaks in through module resolution, and the resulting failure (if deps
  are not cached) is again fatal and named.
- Each source's header entry records `"extraction": {"go": "semantic",
  "python": "syntactic", ...}` for exactly the languages that produced
  packages, so a consumer can see per source what depth of truth it got.
  Values come from a fixed table (go: semantic; python/java/typescript/
  javascript: syntactic), not from runtime discovery.

Python/Java/TS extraction is already dependency-free and unaffected.

## Part 2 - comparison semantics

Both modes consume the two in-memory manifests; neither reparses source.
Only function-like symbols (`func`, `method`, `constructor`) participate in
correspond; drift covers every symbol.

### Mode selection

`--mode drift|correspond` explicit; default is **drift when the two sources
share identity** - same canonical remote URL, same local repo path, or same
non-empty Go `module` path in both manifests - **else correspond**. The chosen
mode is recorded in the header; the auto rule never overrides an explicit
flag.

### `--mode drift`

Matching is by identity:

- Packages by `ID` (Go import path / Java package / relative dir for
  Python/TS). Packages unmatched by ID are then matched by `Path` (catches a
  fork that renamed its module in go.mod); a path-match is reported with
  `"renamed_from_id"` so it is visibly weaker.
- Symbols within a matched package by symbol `ID` (already
  owner-qualified: `Type.Method`).

Reported per symbol, as an exhaustive change-kind sum in Go+ and a string
enum on the wire: `added`, `removed`, and for surviving symbols any of
`kind`, `signature`, `visibility` (covers visibility + idiom), `entrypoint`,
`complexity` (time/space/method triple), `calls` (edges keyed by
target+kind+ref; added/removed edge lists included). `Location` and `Doc`
changes are deliberately **not** reported (every edit moves lines; doc drift
is not API drift). Package-level: added/removed packages listed with symbol
counts; a removed package does not re-list every symbol as removed.

Signature comparison is structural equality over `schema.Signature`
(params/returns/type-params/receiver/variadic/modifiers/throws), ignoring
`Param.Name` changes when `--ignore-param-names` is set (default: names count,
because they are API surface in Python and docs everywhere).

Drift is exact, cheap (two sorted walks), and trivially deterministic.

### `--mode correspond`

Output is **ranked candidate pairs for human review** - the JSON field is
named `candidates`, never "matches".

**Eligibility (triviality threshold):** a symbol participates iff it is
function-like and its body spans `>= --min-lines` source lines
(`EndLine - Line + 1`, default **5**; symbols with `EndLine == 0` - e.g.
signature-only extractions - are excluded). This is the documented gate that
keeps getters, one-line delegators, and empty constructors out of every
ranking.

**Candidate generation (blocking):** scoring every N x M pair is quadratic
blow-up on real repos, so a pair is scored only if it shares at least one
name token (see signal 2) **or** has an identical signature-shape key
(signal 1's normalized vector). Both indexes are hash-join maps, so
generation is near-linear in practice; the blocking rule is part of the
documented, deterministic contract (a pair with no shared token and a
different shape would score below any sane threshold anyway).

**Signals** - each an integer 0-1000, computed in pure integer arithmetic
(scaled rationals, `a*1000/b` with fixed evaluation order; no floats
anywhere):

1. **Signature shape** (`signature`): each param and return position
   normalizes through the shared type-kind vocabulary below. Score:
   arity agreement and positional kind agreement, computed as
   `1000 * (2*|LCS of kind sequences|) / (lenA + lenB)` over the
   concatenated `params ++ ["/"] ++ returns` kind sequence (Dice over an
   order-preserving LCS keeps `(string, int)` vs `(int, string)` close but
   not identical). Variadic status is part of the shape key used for
   candidate blocking.
2. **Name similarity** (`name`): identifiers token-split on camelCase /
   PascalCase / snake_case / kebab / digit boundaries, lowercased; score is
   Dice over token multisets: `1000 * 2*|A intersect B| / (|A| + |B|)`. So
   `validateEmailAddress` vs `email_address_is_valid` share
   {email, address, valid~validate?} - tokens are also stemmed by the single
   cheap rule "strip one trailing `s`/`ed`/`ing`" (documented; no Porter
   stemmer, no locale dependence).
3. **Call-neighborhood overlap** (`calls`): each symbol's callee set
   normalizes to bag-of-tokens: for every call edge, take the final
   identifier of `Target` (after `.`/`::`/`/`), strip a fixed per-language
   stdlib alias table (e.g. Go `fmt.Println` / Python `print` / Java
   `System.out.println` all normalize to `print`; the table ships in
   `internal/diff` and is versioned with the tool), then token-split as in
   signal 2. Score is Dice over the two multisets. When **either** side has
   zero call edges the signal is not computable and is emitted as JSON
   `null`, never 0 (a JVM-style empty `calls` must not read as "no overlap").

**Combined:** fixed integer weights - signature 350, name 400, calls 250 -
with absent signals removed and the remaining weights renormalized in
integer math (`sum(w_i * s_i) / sum(w_i)`). Weights are recorded in the
output header. Ranking: combined desc, then `a.ref`, then `b.ref`
lexicographic - a total order, so output is byte-stable.

**Filters:** `--min-score` (default **400** combined) and `--top` (default
**200** pairs) bound the output; both recorded in the header. Cross-language
pairs are scored by the same machinery and tagged `"same_language": false`
so consumers (and the text renderer, which groups them separately) can
filter to mechanically-extractable same-language pairs;
`--same-language-only` drops cross-language pairs entirely.

### Shared cross-language type-kind vocabulary

One vocabulary, following the `visibility_idiom` precedent (a single wire
enum + per-language normalization at the edge). Go+ enum `TypeKind`, wire
strings:

| Kind | Go | Python (as written) | Java | TS/JS |
|---|---|---|---|---|
| `scalar` | bool, int*, uint*, float*, complex*, rune, byte | int, float, bool, complex | primitives + boxes (int, Integer, double, ...) | number, boolean, bigint |
| `string` | string | str | String, CharSequence | string |
| `collection` | slice, array, chan?no - `[]T`, `[N]T` | list, tuple, set, frozenset | Collection/List/Set/Queue/arrays `T[]` | `T[]`, Array, Set, tuple types |
| `map` | `map[K]V` | dict | Map and subtypes | Map, Record<...>, index signatures |
| `error` | error | *Error/*Exception suffix, `throws` clauses | Throwable/*Exception/*Error | Error and `*Error` |
| `func` | `func(...)` | Callable, lambda ann. | functional-interface names (Function, Consumer, ...) | arrow/function types |
| `object` | named struct/interface/pointer-to-named | any other named class | any other reference type | any other named/object type |
| `unknown` | - | missing annotation | - | any, missing annotation |

Normalization is syntactic over the type string *as recorded in the
manifest* (strip `*`/`?`/generics to the head symbol, then table lookup;
unqualified names use the tail identifier). `func` and `unknown` extend the
brief's seven kinds: `func` because higher-order shape is a strong signal in
exactly the utility functions this tool hunts, `unknown` because syntactic
Python/TS is honestly unannotated, and folding that into `object` would
manufacture agreement. Go's multi-return `(T, error)` keeps the `error` as a
return position; Java/Python throws/raises are **not** synthesized into
return positions (they are declared, not positional) - the `throws` list is
ignored by shape in v1.

## Part 3 - output

### Files and formats

- Default: write `assayxport-diff.json` in the **current working
  directory** (not inside either source). `--out <dir>` overrides the
  directory, mirroring `assay`.
- `--stdout`: print the JSON document, write nothing.
- `--format text`: print a human summary to stdout *and skip the file* -
  text is a view, not an artifact; JSON remains the only machine contract.
  (`--format json` is the default and writes the file as above.)
- Serialization goes through `emit`'s existing `marshal` discipline
  (2-space indent, no HTML escaping, trailing newline); all slices sorted
  by documented total orders; no maps marshaled without ordered
  wrapping; no timestamps; no absolute paths; labels only.

### JSON schema (`assayxport-diff/1`)

```jsonc
{
  "schema_version": "assayxport-diff/1",
  "tool": "assayxport",
  "mode": "drift",                       // or "correspond"
  "mode_selected_by": "auto",            // "auto" | "flag"
  "sources": [                            // exactly two, argument order
    {
      "label": "github.com/owner/repo#3f5981a2c4d1",
      "kind": "remote-git",              // "dir" | "local-git" | "remote-git"
      "commit": "3f5981a2c4d1...40hex",  // omitted for kind "dir"
      // NOTE (implementation amendment): the ref-as-typed is deliberately
      // NOT recorded -- it is spec input, not result, and including it would
      // break the "two specs naming one commit are byte-identical" golden.
      "languages": ["go", "python"],
      "extraction": { "go": "semantic", "python": "syntactic" }
    },
    { "label": "lib#9c2ff0aa11b2", "kind": "local-git", ... }
  ],
  "options": {                            // everything that shaped the result
    "lang": ["go", "python"],            // omitted when unrestricted
    "min_lines": 5,                       // correspond only
    "min_score": 400,
    "top": 200,
    "weights": { "signature": 350, "name": 400, "calls": 250 },
    "ignore_param_names": false,          // drift only
    "same_language_only": false
  },

  // mode "drift":
  "drift": {
    "packages": {
      "added":   [ { "id": "x/y/z", "symbol_count": 12 } ],
      "removed": [ { "id": "x/old", "symbol_count": 3 } ],
      "changed": [
        {
          "id": "goforge.dev/assayxport/internal/emit",
          "renamed_from_id": "old/module/internal/emit",  // path-matched only
          "symbols": {
            "added":   ["WriteAll"],
            "removed": ["writeLegacy"],
            "changed": [
              {
                "id": "Manifest",
                "changes": ["signature", "complexity"],
                "signature": { "a": "func(pkgs []Package) Index",
                               "b": "func(pkgs []Package, module string) Index" },
                "complexity": { "a": {"time": "O(n)"}, "b": {"time": "O(n^2)"} },
                "calls": { "added": [{"target": "sort.Slice", "kind": "external"}],
                            "removed": [] }
              }
            ]
          }
        }
      ]
    },
    "summary": { "packages_added": 1, "packages_removed": 1,
                 "symbols_added": 4, "symbols_removed": 2, "symbols_changed": 7 }
  },

  // mode "correspond":
  "correspond": {
    "eligible": { "a": 412, "b": 388 },   // symbols past the triviality gate
    "candidates": [
      {
        "a": { "ref": "pkg-id#symbol-id", "language": "go",
               "name": "ValidateEmailAddress", "lines": 24 },
        "b": { "ref": "pkg-id#symbol-id", "language": "python",
               "name": "email_address_is_valid", "lines": 19 },
        "same_language": false,
        "scores": { "signature": 812, "name": 640, "calls": null,
                     "combined": 720 }
      }
    ]
  }
}
```

`ref` reuses the manifest's existing `<package-id>#<symbol-id>` link syntax,
so a consumer holding the two full manifests (rerunnable from the header)
can join every row back to complete symbol records. The diff document
deliberately does not embed full symbols - it stays small and the manifests
stay the single source of symbol truth.

### Exit codes

- `0` - ran to completion (differences or not, without `--exit-code`;
  with it: ran and found none / no candidates above threshold).
- `1` - only with `--exit-code`: sources differ (drift) or at least one
  candidate met `--min-score` (correspond).
- `2` - operational failure (spec parse, resolution, git, extraction).
  `runDiffCmd` returns a typed error so `main` can distinguish; the current
  blanket `os.Exit(1)` in `main` is preserved for every other subcommand.

## CLI surface

```
ax diff <source-a> <source-b> [flags]
  --mode drift|correspond      (default: auto by shared identity)
  --label-a / --label-b        override display labels
  --offline                    cache only; no ls-remote, no clone, GOPROXY=off
  --cache-dir <dir>            (default: os.UserCacheDir()/assayxport)
  --lang <l>                   restrict languages (repeatable, registry aliases)
  --out <dir>                  where assayxport-diff.json is written (default .)
  --stdout                     print JSON, write nothing
  --format json|text           (default json)
  --exit-code                  git-diff-style exit 1 when differences found
  --min-lines <n>              correspond triviality gate   (default 5)
  --min-score <n>              correspond combined floor    (default 400)
  --top <n>                    correspond result cap        (default 200)
  --same-language-only         drop cross-language candidates
  --ignore-param-names         drift: parameter renames are not signature drift
  --quiet                      suppress progress on stderr
```

## Tests

- **Spec parsing:** table-driven over every grammar form plus malformed input
  (empty ref, `#` on a URL fragmentless remote, scp-style with ref, `#` in
  the middle of a path, empty spec).
- **Resolver:** constructs real repos in `t.TempDir()` via the `git` binary
  (`git init`/`commit`/`tag`; helper skips when git is absent). Asserts:
  archive materialization leaves HEAD/index/stash/worktree byte-identical
  (snapshot `git status --porcelain` + HEAD before/after); ref forms (tag,
  branch, SHA, `HEAD~1`) resolve to the expected commits; cache hit performs
  no fetch (second resolve with `GIT_TERMINAL_PROMPT=0` and PATH stripped of
  git... simpler: with the remote URL pointed at a moved/deleted dir);
  `--offline` behavior for seen and unseen refs. Remote tests use
  `file://`-cloned local bare repos - the real git transport, no network.
- **Drift:** fixture pair of source trees exercising every change kind;
  assert the exact JSON.
- **Correspond:** fixtures with a known near-duplicate across Go and Python;
  assert candidate order, per-signal scores, null calls signal, threshold
  and blocking behavior. Property tests (rapid): score range [0,1000],
  symmetry of name/calls signals, determinism across shuffled input order.
- **Golden:** (1) run the same diff twice, assert byte-identical files;
  (2) resolve one source by branch name and by its SHA, assert
  byte-identical output (labels resolve to the same `#sha12` form).

## Implementation order (reviewable increments)

1. `internal/source` + full test suite (parse + resolve + cache + offline).
2. `ax diff --mode drift` end-to-end: manifest pair via registry/emit,
   drift matcher, JSON + text render, exit codes, goldens.
3. `--mode correspond`: type-kind vocabulary, tokenizer, three signals,
   blocking, ranking, its goldens and property tests.

Each lands with `go generate ./...` clean, `go vet`, race-enabled tests, and
`go tool goplus gen -check .` passing.

## Non-goals (restated)

Clustering >2 sources; anti-unification / extraction / codegen; embeddings or
any model call; vendored git; schema/validation-specific analysis. JS/TS is
**in** scope only because the extractor already exists; nothing here is
specific to it.
