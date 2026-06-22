# nugit knowledge — index

> Open this folder as an [Obsidian](https://obsidian.md) vault (or any Markdown editor).
> These are the same files git tracks — editing a note **is** a git change.
> Regenerate this index with `nugit obsidian`.

## Specs

- [[SPEC-001-thin-keystone|SPEC-001 — Thin keystone: `nugit pr-render`]]
- [[SPEC-002-integration-contract|SPEC-002 — External tool integration contract]]

## Decisions

- [[0001-schema-versioning|ADR-0001 — Stable cross-reference keys + schema versioning]]
- [[0002-file-to-component-binding|ADR-0002 — File→component binding via `properties { paths }` globs]]
- [[0003-supersede-without-mutation|ADR-0003 — Supersede without mutation; status is derived, not flipped]]
- [[0004-thin-keystone-first|ADR-0004 — Ship the keystone first; defer infrastructure to its trigger]]
- [[0005-squash-merge-capture|ADR-0005 — Capture survives squash-merge]]
- [[0006-per-pr-cost-budget|ADR-0006 — Per-PR compute budget; deterministic by default]]
- [[0007-hard-delete-and-gc|ADR-0007 — Hard-delete / erasure path despite immutable-in-git]]
- [[0008-brownfield-bootstrap|ADR-0008 — Bootstrap the C4 model from the import graph, default to warn]]
- [[0009-git-root-coordinates-and-structural-mode|ADR-0009 — Git-root-relative coordinates + language-agnostic structural mode]]
- [[0010-a-dedicated-doctor-pre-flight-separate-from-pr-ren|ADR-0010 — a dedicated doctor pre-flight separate from pr-render]]
- [[0011-external-tool-integration-single-writer|ADR-0011 — External-tool integration: single-writer-per-fact, one-way flows]]

## Lessons

- [[dogfooding-a-health-check-finds-real-gaps-nugit-s|LESSON-dogfooding-a-health-check-finds-real-gaps-nugit-s — Lesson — dogfooding a health-check finds real gaps (nugit's own repo was missing the comm…]]
- [[read-from-reviewed-ref|LESSON-read-from-reviewed-ref — Lesson — a PR-time analyzer must read every artifact from the reviewed ref]]

## Glossary

- [[glossary|GLOSSARY — Glossary]]

