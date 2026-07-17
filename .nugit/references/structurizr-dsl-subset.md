---
schema_version: 1
id: REF-structurizr-dsl-subset
type: reference
scope: c4
status: active
created: 2026-07-02T00:00:00Z
relates_to:
  - informs:ADR-0002
provenance:
  commit: seed
  citation: internal/c4/c4.go
confidence: high
source: https://docs.structurizr.com/dsl/language
---

# Structurizr DSL — the subset nugit parses, and the renderer's stricter rules

Claims distilled for this project (full language reference at `source`):

- nugit parses a **subset**: `workspace / model / softwareSystem / container /
  component / group`, `->` relationships, and `properties` blocks. Views,
  styles, themes, `!include`, and identifiers-with-dots pass through unparsed —
  do not rely on them for `paths` binding.
- `properties { "paths" "glob" }` is a Structurizr-legal generic key/value
  block; nugit's use of it for file→component binding (ADR-0002) piggybacks on
  valid DSL, so models stay renderable by real Structurizr tooling.
- The **real Structurizr renderer is stricter than nugit's parser** (verified on
  the pilot model): it requires `system → container → component` nesting and
  multi-line quoted properties — `properties {\n  "paths" "x"\n}` — inline
  `properties { paths "x" }` is a parse error there even though nugit accepts
  it. Emit the multi-line form everywhere (`bootstrap` does).
- Component identifiers are the `affects:`/`constrains:`/scope link targets;
  renaming one silently orphans every knowledge object scoped to it — treat DSL
  identifier renames as knowledge migrations, not cosmetic edits.
