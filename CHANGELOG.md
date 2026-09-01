# Changelog

## Unreleased

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
- Add the domain-neutral `assayxport.trace/v3` artifact and relation graph.
- Add stable declaration IDs, semantic change detection, and release-note to
  ADR to deployable-code closure verification.
- Add `ax verify` without changing the released v2 API manifest format.

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
