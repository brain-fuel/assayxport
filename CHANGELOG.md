# Changelog

## Unreleased

- Add the domain-neutral `assayxport.trace/v3` artifact and relation graph.
- Add stable declaration IDs, semantic change detection, and release-note to
  ADR to deployable-code closure verification.
- Add `ax verify` without changing the released v2 API manifest format.

## v0.19.3

- Migrate the explorer loader's production scheduling policy from Cadence's
  legacy strategy vocabulary to validated v0.4 execution plans.
- Derive fetch priority from the plan activation axis.
- Resolve Cadence v0.4.0 as a released dependency with no local replacement.
