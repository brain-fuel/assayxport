# Changelog

## v0.27.0

- `ax publish` gains target flags `--npm --mvn --pypi --go` (and `--cargo`,
  registered as the pattern for later: `[TARGET_NOT_IMPLEMENTED]`), plus `--all`
  for the config's `targets` list, `--npmrc-path` and `--pypirc-path`. With no
  flag the command behaves exactly as before. With flags, only those targets
  run (still behind the clean-checkout and tag gates), each checking its own
  configuration and credentials in one preflight before anything is uploaded.
- New `Target` abstraction in `internal/publication`. `mvn` wraps the Central
  flow unchanged; `go` wraps the proxy and pkg.go.dev verification and, with
  `[go] tag_prefix`, creates and pushes only the annotated tag
  `<prefix>v<version>` (never forced; `GO_REMOTE_TAG_MISMATCH` if origin has it
  elsewhere); `npm` publishes through `npm publish` with the terminal attached
  and compares `dist.integrity`; `pypi` uploads natively to the legacy API and
  compares sha256 from the JSON API. All are idempotent and resumable, with
  state in `.assayxport/releases/<version>.<target>.json`.
- New codes: `NPM_CREDENTIALS_MISSING`, `PYPI_CREDENTIALS_MISSING`,
  `NPM_VERSION_MISMATCH`, `PYPI_FILE_MISMATCH`, `TARGET_NOT_IMPLEMENTED`, and
  per-target input codes (`NPM_TARBALL_*`, `PYPI_*`, `GO_*`).
- Config `schema_version = 2` adds `[npm]`, `[pypi]`, `[go] tag_prefix` and a
  root `targets` list; schema 1 files parse as before and reject the new keys
  with `CONFIG_KEY_UNSUPPORTED`. Schema 2 also lets a `maven-repository`
  release name `go.module`.
- Credential files keep the owner-only (0600) check on Unix; on Windows the
  check is skipped with a warning.

## v0.26.0

- `maven-repository` releases may span several groups: an `artifacts` entry is
  `artifactId` (group `group_id`) or `groupId:artifactId`, and every group must
  be `group_id` or a subgroup of it. New codes `REPO_GROUP_OUTSIDE_NAMESPACE`
  (config) and `STAGING_GROUP_OUTSIDE_NAMESPACE` (staged files). The staging
  scan attributes files by their last three path components, so a subgroup's
  directory nested under another artifact's directory is read correctly. This
  carries a Gradle plugin and its marker POM in the same bundle.
- Gradle Module Metadata (`.module`) is explicitly a publishable file: signed,
  checksummed, and never reported as unexpected.

## v0.25.1

- `ax publish` no longer cuts a Central bundle upload off after two minutes:
  the upload has no whole-request deadline, only bounds on connecting and on
  Central's answer once the bundle is sent. A bundle of hundreds of megabytes
  (a Maven reactor with native binaries) timed out mid-upload.

## v0.25.0

- Add `ax publish` policy `maven-repository`: publish every artifact of a
  Maven reactor from a build-staged, unsigned repository-layout directory as
  one deterministic signed bundle. Preflight checks the named and staged sets,
  versions and SNAPSHOTs, POM coordinates, main/sources/javadoc jars, and
  inherited Central metadata, and rejects build signatures. It never runs the
  Go release verification.
- Add `ax publish --config <path>` and an optional checkout-directory argument
  so the configuration can live outside a tagged, immutable checkout; the
  clean-checkout and tag gates are unchanged.
- Add the domain-neutral `assayxport.trace/v3` artifact and relation graph.
- Add stable declaration IDs, semantic change detection, and release-note to
  ADR to deployable-code closure verification.
- Add `ax verify` without changing the released v2 API manifest format.

## v0.24.0

- Add `ax diff <a> <b>`: assay two sources and report how they relate as a
  deterministic `assayxport-diff.json`. Sources are directories, local repos
  at a `#ref`, or remote repos (shallow-fetched via the system git into a
  commit-addressed cache; `--offline` uses only the cache). `--mode drift`
  reports exact per-symbol changes (signature, visibility, entrypoint,
  complexity, call edges) between related sources; `--mode correspond` ranks
  similar function pairs across unrelated codebases by signature shape, name
  tokens, and call-neighborhood overlap, on an integer 0-1000 scale with no
  network or model calls. `--exit-code` follows git-diff convention with
  exit 2 reserved for operational failure. Extraction failures are fatal for
  diff (never a silently partial comparison); `--lang` restricts languages
  explicitly and is recorded in the output header.

## v0.23.0

- Node/JavaScript/TypeScript: read package.json manifests (root and
  workspaces). npm `bin` targets and node-shebang scripts become entrypoints
  with `npx <bin>` / `node <file>` invocations, mirrored onto a top-level
  `main` function; entry modules named by `main`/`module`/`exports` inherit
  the package description as their doc.
- Extract CommonJS exports (`exports.name`, `module.exports.name`, and
  wholesale `module.exports = {...}` / `= fn`) as exported symbols with
  visibility idiom `commonjs-export`.
- Represent `export default` of identifiers, literals, and anonymous
  functions/classes as a `default` symbol.
- Honor `private`/`protected`/`#name` modifiers on class methods (previously
  fields only); a method merely named `privateFoo` stays public.
- Accept `--lang` aliases: `javascript`, `js`, `ts`, and `node` select the
  TypeScript extractor; `golang` and `py` also resolve.

## v0.19.3

- Migrate the explorer loader's production scheduling policy from Cadence's
  legacy strategy vocabulary to validated v0.4 execution plans.
- Derive fetch priority from the plan activation axis.
- Resolve Cadence v0.4.0 as a released dependency with no local replacement.
