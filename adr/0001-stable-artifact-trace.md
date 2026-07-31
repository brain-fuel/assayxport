---
artifact_id: adr.trace.stable-artifacts
artifact_kind: adr
decides:
  - schema.trace.artifact-id
  - schema.trace.validate
  - schema.trace.verify-release
  - cli.verify
status: accepted
---

# Stable artifact trace

## Context

Source paths and line numbers change during ordinary maintenance. They are not
durable identities. A release still needs to identify code that changed without
a recorded reason.

## Decision

Each deployable declaration has a stable logical ID. Assayxport stores paths as
locations only. It compares normalized semantic digests between release locks.
A release note must announce an ADR that decides each changed deployable unit.

## Consequences

Moving unchanged code does not create a false change. Removing or splitting a
unit requires explicit lineage. Generated code inherits its source identity.
