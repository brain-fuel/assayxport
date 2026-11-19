# assayxport

> Assaying analyzes a metal to report its exact composition. `assayxport`
> analyzes a codebase to report its exact API composition - where each symbol
> lives, what package it belongs to, its signature, its docs, whether it is a
> runnable entrypoint, and who it calls - as a deterministic JSON manifest at
> the project root, so an LLM, docgen, or tool reads one map instead of
> reparsing everything.

Part of the [goforge](https://goforge.dev) suite. Supports Go (native
`go/packages`), Python, Java, TypeScript, and JavaScript source, plus compiled
JVM artifacts. One scan produces a deterministic manifest through the same
schema and explorer regardless of how declarations were acquired.

## Install

```bash
go install goforge.dev/assayxport/cmd/ax@latest
```

The binary is `ax` — short for AssayXport, and short to type.

## Trace graph preview

The unreleased trace foundation keeps durable artifact identity separate from
file paths and line numbers. A release can compare semantic unit digests with
its prior lock and reject changed deployable code that has no
release-note-to-ADR path.

```bash
ax verify assayxport.trace.json
ax verify assayxport.trace.json \
  -baseline previous.trace.json \
  -release release.v0.20.0
```

This graph is domain-neutral. A consumer such as GoTP owns its compatibility
claims and refers to assayxport artifact IDs for source and test evidence.

## Use

```bash
ax scan .                # writes assayxport.json + .assayxport/ shards
ax scan ./pkg --stdout   # print combined JSON, write nothing
ax scan . --lang java    # restrict to one language (repeatable)

# Compiled JVM API (the preferred command name is `assay`; `scan` is an alias)
ax assay ./slf4j-api-2.0.17.jar --stdout
ax assay mvn:org.slf4j:slf4j-api:2.0.17 --stdout
ax assay mvn:com.example:widget:1.2.3 --out ./report
```

### Compiled JVM artifacts

Local JAR and Maven scans parse `.class` files directly in Go. They do not
execute or load classes, resolve referenced dependencies, invoke Java, or need
ASM/JDK tooling. Public types and public/protected members are emitted as normal
Java package and symbol records, including descriptors, generic signatures,
declared exceptions, annotations, constants, records, sealed hierarchies, and
nested types. Synthetic and bridge declarations are suppressed.

Maven Central is the default repository. Any Artifactory, Nexus, or other
Maven-layout repository can be selected without vendor-specific integration:

```bash
ax assay mvn:com.example:widget:1.2.3 \
  --maven-repo https://artifactory.example.com/artifactory/libs-release
```

Artifacts are read from and atomically cached in `~/.m2/repository`; override
that with `--maven-cache`. Basic or bearer authentication is accepted through
`--maven-user`/`--maven-password` and `--maven-token`, or the corresponding
`AX_MAVEN_USERNAME`, `AX_MAVEN_PASSWORD`, and `AX_MAVEN_TOKEN` environment
variables. Credentials are never included in manifests or errors. Timestamped
release artifacts are supported; unresolved `-SNAPSHOT` coordinates are
rejected with an actionable error.

Multi-release JARs honor `Multi-Release: true` and choose, per class, the
highest definition no newer than `--java-release`. The deterministic default is
Java 25 and does not depend on an installed JDK. Versioned entries in ordinary
JARs are ignored.

Classfiles normally contain no Javadoc or honest source location and this
extractor does not fabricate either. Locations identify the stable JAR entry;
calls and complexity remain empty/deferred because declaration scanning does
not interpret bytecode. Use source scanning when docs, source lines, syntactic
calls, or source-derived complexity are required.

### Languages

By default `scan` runs every registered source extractor (Go, Python, Java,
TypeScript/JavaScript) and merges the results into one manifest whose
`languages` field lists what was found. Use
`--lang` to restrict the run; it is repeatable, so `--lang python --lang go`
runs only those two. An unregistered language name is an error that lists the
available ones.

Go extraction is fully semantic via `go/packages`. Python and Java extraction is
syntactic (tree-sitter): names, kinds, visibility, signatures as written, doc
comments, decorators/annotations, and entrypoints are reliable; imported-name
resolution and inferred types are not.

### Node / JavaScript / TypeScript

TS/JS scans understand the Node ecosystem, not just the syntax. `package.json`
manifests (the root one and any workspace ones, discovered with the same skip
rules as source) contribute:

- **Entrypoints**: npm `bin` targets (string or map form) and files opening
  with a node shebang are marked `is_entrypoint`, with an invocation hint
  (`npx <bin>` or `node <file>`) mirrored onto a top-level `main` function.
- **Entry modules**: files named by `main`, `module`, or any string leaf of
  the `exports` map inherit the package description as their module doc.

Both module mechanisms are extracted: ES module `export` declarations
(including `export default` of named, anonymous, and referenced values) and
CommonJS assignments (`exports.name`, `module.exports.name`, wholesale
`module.exports = {...}`), the latter tagged `visibility_idiom:
"commonjs-export"`. Class member visibility honors `private`/`protected`
modifiers and `#private` names. `--lang javascript`, `js`, `ts`, and `node`
all select this extractor. `node_modules`, build output, and `.d.ts` ambient
declarations are never scanned; a malformed `package.json` degrades gracefully
rather than failing the scan. Java visibility is the 4-way access
modifier (`public`/`protected`/`private`/`package-private`); consumers read the
per-symbol `visibility_idiom` to interpret the `visibility` field across
languages.

## Output

- `assayxport.json` - root index: project metadata + one entry per package.
- `.assayxport/<package-dir>.json` - per-package shard with the full symbol list.

Output is deterministic: relative paths, no timestamps, stable ordering. Equal
inputs produce byte-identical files.

## Compare two sources

`ax diff` assays two sources and reports how they relate as a deterministic
`assayxport-diff.json` (or `--stdout`, or `--format text` for a summary).
Each source is a local directory, a local repo at a ref (`#` separator), or
a remote repo — any combination:

```bash
ax diff . ../fork                                  # two working trees
ax diff .#v1.2.0 .#main                            # two refs of one repo
ax diff . https://github.com/owner/repo#main       # local vs remote
ax diff git@github.com:a/x.git https://github.com/b/y   # remote vs remote
```

Local refs are materialized with `git archive` into a scratch directory —
the working tree, index, stash, and HEAD are never touched. Remotes resolve
via `git ls-remote` and shallow-fetch through the system git binary (your
credential helpers, ssh agent, and proxy config apply; credentials are never
prompted for) into a commit-addressed cache; `--offline` forbids the network
and uses only the cache. Labels are stable (`host/owner/repo#<sha12>`) and
the output never contains filesystem locations; `--label-a`/`--label-b`
override them.

Two modes, because two questions:

- **`--mode drift`** — for sources that share identity (two refs, a fork and
  its upstream). Matches packages and symbols by id and reports exact
  changes: added, removed, signature, visibility, entrypoint status,
  complexity estimate, call edges. Auto-selected when both sources resolve
  to the same repository or Go module.
- **`--mode correspond`** — for unrelated codebases. Ranks candidate
  function pairs for human review (never asserted equivalence) by three
  separately-reported integer signals: normalized signature shape, name
  token similarity, and call-neighborhood overlap. Deterministic and fully
  offline — no embeddings, no model calls, integers on a 0–1000 scale.
  Trivial symbols (below `--min-lines`, default 5) are excluded;
  cross-language pairs are scored but tagged, and `--same-language-only`
  drops them.

The output header records each source's resolved commit, the extraction mode
per language (`semantic` for Go, `syntactic` for tree-sitter languages), the
diff mode, and every threshold that shaped the result, so a diff is
reproducible from its own header. Unlike `ax assay`, a per-language
extraction failure is **fatal** — a diff over a half-loaded package graph
would be confidently wrong — and the only way to skip a failing language is
an explicit `--lang` restriction, which the header records. `--exit-code`
follows git-diff convention (1 = differences found), with exit 2 reserved
for operational failure so CI can tell the two apart.

## Call graph

Every function-like symbol carries a `calls` list: its distinct callees, each
with a call-site `count`, sorted for byte-stable output. The graph is stored
as edges-at-the-symbol, so the whole-program graph assembles by following
`ref` links from shard to shard, and it bottoms out - all the way down to
language primitives, or as far as the project's semantics allow - at the four
non-internal kinds:

- `internal` - the callee is a symbol in this manifest; `ref` locates it as
  `<package-id>#<symbol-id>`. Traversal continues here.
- `builtin` - a language primitive (Go `len`/`make`/`append`..., Python
  `print`/`len`/`range`...). The floor of the graph.
- `external` - resolved to a package outside the scan (stdlib or a
  dependency). The scan boundary.
- `dynamic` - a call through a func value or interface method; the static
  target is unknowable. An interface-method call still names (and, when the
  interface is scanned, links) the interface method itself.
- `unresolved` - a syntactic extractor ran out of information; the callee is
  recorded as written.

Resolution depth follows extraction depth. Go edges are fully semantic
(`go/types`): package functions, methods, builtins, and interface dispatch
are classified exactly, and type conversions `T(x)` are never mistaken for
calls. Python and Java edges are syntactic and honest about it: bare names
resolve against the module's own defs/classes (Python) or the file's own
types and their enclosing-type chain (Java), then the top-level import table
(aliases expanded: `np.array` reports as `numpy.array`), then the builtin /
`java.lang` floor; a receiver-typed call (`obj.method(...)`) whose receiver
is not the method's own `self`/`cls`/type is `unresolved` because inheritance
and instance types are invisible to syntax. `super` in Java is always
`unresolved` for the same reason.

Java edges additionally record the call site as written, so overloads can be
drawn accurately: `arity` is the argument count, and `arg_types` is
per-position type evidence - the entry is the type when the source states it
(a literal, a cast, a `new T(...)`, `this`, a string concatenation) and
`null` when it does not (an identifier, a call result). Casts at call sites
are the programmer's own overload disambiguation, and the vector preserves
them exactly. Deduplication is keyed on the full vector, so `f(1)` and
`f(x)` stay distinct edges into an overloaded name; a consumer joins the
evidence against each candidate overload's `signature.params` and gets an
exact edge when arity or evidence distinguishes, an honest fan-out when it
does not. Evidence never upgrades a kind - no widening, boxing, or hierarchy
is assumed - but it can downgrade one: a call whose arity matches no locally
declared overload is `unresolved` rather than linked to the wrong local
method (the real callee is likely inherited). Varargs accept `>= n-1`
arguments; a record's canonical constructor counts as declared, arity taken
from its components.

Closures, lambdas, and nested defs are not symbols of their own, so calls
made inside them are attributed to the enclosing declared function - an edge
behind a `defer func() { ... }()` or a lambda argument is not lost, but the
graph does not record that its execution is conditional on the closure
running. Overloads of a Java method share one manifest id; the `arity` and
`arg_types` on each edge (and `signature.params` on each declaration) are
what tell them apart.

## Complexity

Every function-like symbol (function, method, constructor) carries a
best-effort big-O estimate in its `complexity` object (`time`, `space`,
`method`). The `method` field says how the estimate was derived:

- `loop-nesting` - a bound derived from the deepest loop nesting in the
  function body. `time` is `O(1)`, `O(n)`, `O(n^2)`, ...; `space` is `O(1)`
  unless an allocation occurs inside a loop.
- `recursive` - the function calls itself, so nesting cannot bound it; `time`
  and `space` are `null` (the estimator refuses to guess).
- `deferred` - the symbol was not analyzed (a type, field, variable, or
  annotation element); `time` and `space` are `null`.

This is a coarse triage signal, not a proof. It is intraprocedural (the cost of
functions this one calls is NOT propagated), it conservatively counts a
constant-bound loop such as `for i := 0; i < 10; i++` as `O(n)`, it never
bounds recursion, and `space` is a weak allocation-based heuristic. Loops
inside a nested closure, lambda, or local class are attributed to that inner
scope, not the enclosing function.

Two honesty notes on the heuristic edges. The `space` allocation signal is not
identical across languages: Go composite literals and Java `new` are counted as
allocations, but a Python constructor call (`Foo()`) is syntactically
indistinguishable from any other call and is NOT counted, so equivalent code
can report a different `space` bound per language.

Recursion detection is scope-aware but syntactic. A self-call from a method
must be qualified (`self.f(...)` in Python, a `recv.f(...)` selector in Go), so
a method that calls an imported or package-level function of the same name is
not mistaken for recursion; a plain function uses the bare-name rule. In Java,
which has no free functions but does have overloading, a self-call must match
both name and argument count, which rejects overload delegation such as
`f(x)` calling `f(x, 0)`. The one case that remains indistinguishable is two
Java overloads of the same arity but different parameter types; there, delegation
may still read as `recursive`. When in doubt the estimator prefers to null the
bound rather than fabricate one.

## Semantic model in Go+

AssayXport keeps its stable JSON structs as an explicit wire boundary. Its
internal invariants are now executable Go+ abstractions:

- resolution, evidence, overload results, complexity, languages, symbol kinds,
  visibility, documentation, invocation, hierarchy nodes, and concerns are
  exhaustive sums;
- package/symbol ids, symbol references, source paths, and shard paths are
  validated opaque values, with distinct position, arity, count, byte,
  version, priority, and worker-count units;
- complete, useful-partial, and failed extraction form one outcome sum;
- streaming, skeleton, and demand extraction are capability classes with a
  generated package-identity law;
- hierarchy summaries are a law-tested monoid, while call normalization is a
  law-tested `goforge.dev/goplus/std/canonical.Canonical` instance;
- progressive packages carry indexed phases, and scheduler/writer transitions
  use quantity-1 permits with generated use-once guards for Go callers.

Committed `_gp.go` files preserve ordinary Go installation. Regeneration uses
Go+ v0.16.1 or newer and `goforge.dev/goplus/std` v0.17.0.

## Publish a Maven reactor to Central

`ax publish` validates release inputs, prepares one deterministic signed
bundle, and uploads it to the Central Portal. It uploads with
`USER_MANAGED`, waits for `VALIDATED`, promotes explicitly, waits for
`PUBLISHED`, and keeps resumable state in `.assayxport/releases/<version>.json`.
Credentials come from `MAVEN_CENTRAL_USERNAME` and `MAVEN_CENTRAL_PASSWORD`,
`maven_settings.xml` in the root, or `--maven-settings-path`. The OpenPGP key
is `AX_MAVEN_SIGNING_KEY`, default
`<user config dir>/assayxport/maven-central-private.asc`.

Policies `goplus-dual` and `java-library` publish one artifact per project.
Policy `maven-repository` publishes every artifact of a Maven reactor that the
project's own build staged, unsigned, in Maven repository layout:

```toml
schema_version = 1
version = "0.36.0"
policy = "maven-repository"
group_id = "dev.goforge"
staging = "hosts/jvm/target/central-staging"   # relative to the root
artifacts = "lawspec-parent,lawspec,lawspec-darwin-arm64"  # or ["a", "b"] on one line
# an entry is artifactId, or groupId:artifactId for a subgroup of group_id, e.g.
# "dev.goforge.lawspec:dev.goforge.lawspec.gradle.plugin" (a Gradle plugin marker)
# bundle = "hosts/jvm/target/central-bundle.zip"  # default: <staging>-bundle.zip
```

The root is the Git checkout (the working directory, or the one directory
argument). Preflight keeps the clean-checkout and `v<version>` tag gates, so
keep `staging` and `bundle` in an ignored build directory. It never runs
`VerifyGoRelease`, and it accumulates every problem with a code and a fix:

| Code | Rejects |
|---|---|
| `STAGING_MISSING`, `STAGING_PATH_INVALID` | no staging directory, or one outside the root |
| `REPO_ARTIFACT_NOT_STAGED`, `REPO_ARTIFACT_NOT_NAMED`, `REPO_ARTIFACT_DUPLICATE` | `artifacts` and the staged set disagree |
| `REPO_GROUP_OUTSIDE_NAMESPACE`, `STAGING_GROUP_OUTSIDE_NAMESPACE` | a named or staged group that is neither `group_id` nor a subgroup of it |
| `STAGING_SNAPSHOT`, `STAGING_VERSION_MISMATCH`, `REPO_POM_SNAPSHOT`, `REPO_POM_PARENT_MISMATCH` | SNAPSHOTs, or any version other than `version` |
| `REPO_POM_MISSING`, `REPO_POM_INVALID`, `REPO_POM_COORDINATE_MISMATCH` | a POM absent, unreadable, or whose groupId, artifactId or version (own or from `<parent>`) differs from its path |
| `REPO_JAR_MISSING`, `REPO_SOURCES_MISSING`, `REPO_JAVADOC_MISSING` | a non-`pom` packaging without a non-empty main, `-sources` or `-javadoc` jar |
| `REPO_POM_METADATA_MISSING` | name, description, url, licenses, developers or scm absent after following `<parent>` within the staged set (name and description are not inherited) |
| `STAGING_SIGNATURE_PRESENT` | a build-made `.asc` |
| `STAGING_FILE_UNEXPECTED`, `STAGING_UNREADABLE` | files outside `<group path>/<artifactId>/<version>/`, or not named `<artifactId>-<version>*` |
| `CONFIG_KEY_UNSUPPORTED`, `BUNDLE_INSIDE_STAGING` | go/maven keys under this policy, or a bundle inside the staging directory |

Signatures are rejected, not ignored: the bundle is signed once with the ax
key, so build with the signer off (Maven: `-Dgpg.skip`). Build checksums
(`.md5`, `.sha1`, `.sha256`, `.sha512`) and Maven's `maven-metadata*.xml` are
ignored and never published; ax regenerates checksums for every file and
signature. Prepare signs each staged file, writes one bundle with the staging
layout, and ordering, timestamps and signatures make it byte-for-byte
reproducible. Upload, validation, promotion and resume are unchanged.

One release can span several groups, as long as each is `group_id` or a
subgroup of it (Central verifies the namespace including subgroups). A Gradle
plugin marker has the plugin id as its group and `<id>.gradle.plugin` as its
artifact, so `dev.goforge.lawspec` stages at `dev/goforge/lawspec/`, the same
directory that holds artifact `dev.goforge:lawspec`'s version directories. The
scan reads a file's group, artifact and version from its last three path
components, so both attribute correctly. Gradle Module Metadata
(`<artifact>-<version>.module`) is published like any other file: signed and
checksummed, never flagged.

`ax publish --config <path>` reads the configuration from another file, so a
release whose immutable tag holds no `assayxport.toml` can still be published:

```sh
ax publish --prepare --config ~/releases/lawspec-0.36.0/assayxport.toml
```

### Staging a Maven build

The Central publishing plugin (`central-publishing-maven-plugin` 0.9.0 with
`<extensions>true</extensions>`) binds `publish` to `deploy`, stages into
`target/central-staging` of the first module that declares it, then bundles
and uploads. Its `skipPublishing` property skips staging as well, so it
cannot produce a staging directory; running `deploy` would also need Central
credentials. Stage with the stock deploy plugin instead, which the extension
leaves callable from the command line. For the LawSpec 0.36.0 reactor:

```sh
mvn -f hosts/jvm -Prelease,all-platforms -Drevision=0.36.0 -Dgpg.skip -DskipTests \
  package deploy:deploy \
  -DaltDeploymentRepository=staging::file:$PWD/hosts/jvm/target/central-staging
```

Remove the staging directory first: it accumulates across builds, and ax
rejects what it finds.

## License

MIT. Third-party components (the tree-sitter runtime and the Python and Java
grammars, all MIT) are attributed in [NOTICE](NOTICE).
