---
schema_version: 1
id: GLOSSARY
type: glossary
scope: global
status: active
created: 2026-06-18T00:00:00Z
provenance:
  commit: bootstrap
confidence: high
---

# Glossary

- **keystone** — the unified PR view (`nugit pr-render`); the one feature with clear
  ROI, shipped first per the re-shaped plan.
- **component → path binding** — the `properties { paths }` glob on each C4 component
  that maps a logical component to physical files. The load-bearing primitive the
  review found missing. See [[0002-file-to-component-binding]].
- **effective status** — a knowledge object's status DERIVED from the supersedes graph
  at read time, never mutated in place. See [[0003-supersede-without-mutation]].
- **amends** — a `relates_to` edge meaning "this decision overrides PART of the target;
  the rest stands." The target stays live, annotated `amended by <id>` at read time —
  partial supersession without mutation or section anchors. See [[0015-partial-supersession-amends]].
- **delta** — a deterministic diff computed from two git refs and committed artifacts:
  one of C4, code, knowledge, plan.
- **consistency check** — a set/graph operation over committed text that makes the PR
  view *verify* rather than merely present.
- **significance tier** — trivial / feature / architectural; drives progressive
  disclosure of the PR view.
- **stale knowledge** — code governed by a `superseded`/`invalidated` object that a PR
  changes without updating the object.
