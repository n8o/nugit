---
schema_version: 1
id: LESSON-an-undeclared-vocabulary-is-documentation-not-behaviour
type: lesson
scope: knowledge
status: active
created: 2026-10-08T00:00:00Z
relates_to:
  - constrains:knowledge
  - informs:ADR-0041
  - reinforces:LESSON-a-tolerant-reader-owes-the-author-a-linter
provenance:
  commit: seed
  citation: "pilot repo: 368 of 393 relates_to edges carry no verb across 162/220 objects; `prevents` and `satisfies` documented in AGENTS.md with zero non-test code references; one `refines:` edge consumed by nothing"
confidence: high
---

# Lesson — an enum that lives only in documentation is documentation, not behaviour

**Trigger:** a schema field whose allowed values are shown as examples in prose
— a relation verb, a status, a kind — and parsed by a reader that accepts
anything.

**Insight:** if the vocabulary is not declared in code, three things are true
at once and none of them is visible to an author. Verbs the engine reads and
verbs it ignores look identical in the store. A typo is indistinguishable from
a working edge, because a tolerant parser returns it happily. And the
documentation drifts into advertising values nothing implements — nugit's own
AGENTS.md told authors to write `prevents:` and `satisfies:`, which have **zero
non-test code references**, and nugit's own store uses `prevents:`.

The pilot's numbers are what undeclared looks like after four months: **368 of
393 `relates_to` edges carry no verb at all**, and of the 25 that do, one is
`refines` — read by nothing. A store whose edges are 94% bare has a
supersession, amendment and reinforcement graph that exists only in prose,
which is exactly the state the typed store was built to replace.

The worst case is a verb that names a field somewhere else. `supersedes:`
written inside `relates_to` parses, resolves in the one-hop traversal, and
declares no supersession — because ADR-0003 derives effective status from the
front-matter *field* of that name. It reads like a lifecycle claim and is
decoration.

Declare the table in the same package as the parser, classify each verb by what
actually consumes it, and report the rest. Keep the tolerance — the schema
belongs to its authors and will grow — but stop being silent about it.

**Rejected:** deleting the unimplemented verbs to make the docs true. They were
documented for months and are in two live stores; demoting honest authorship to
"unknown" produces a wall of warnings and teaches people to mute the check.
Classify them as annotation-only and say so.

**Keywords:** vocabulary, enum, relates_to, edges, silent tolerance, parser,
documentation drift, supersedes, lint, schema
