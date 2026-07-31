---
artifact_id: spec.trace.v3
artifact_kind: specification
documents:
  - adr.trace.stable-artifacts
---

# Trace v3 specification

The trace schema is `assayxport.trace/v3`. Artifact identity is the tuple of
module and artifact ID. Location is not part of identity.

A valid graph has unique artifact IDs, valid relation endpoints, a governing
ADR for each authored deployable code unit, and a generator relation for each
generated unit.

A valid release has a release-note artifact. Each added, changed, or removed
deployable unit must have this path:

```text
release note -> announces -> ADR -> decides -> code unit
```

The symbol manifest remains schema v2 until trace extraction is integrated into
the normal assay emitter.
