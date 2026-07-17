---
schema_version: 1
id: ADR-0014
type: decision
scope: global
status: accepted
created: 2026-07-02T00:00:00Z
relates_to:
  - constrains:knowledge
  - constrains:retrieval
  - elaborates:ADR-0011
provenance:
  commit: seed
  citation: internal/model/model.go KindReference; pilot review 2026-07-02
confidence: high
---

# ADR-0014 — `reference`: a fifth durable type for distilled external knowledge

## Context

The four durable types are all *project-authored*: a lesson needs an incident,
an ADR needs a decision, a spec needs intended behavior, the glossary holds
vocabulary. External technical material — papers, vendor docs, standards,
benchmarks — that shapes the project has no home. The pilot hit this
directly: standards and vendor research informing ADRs had nowhere to live except
squeezed into ADR Context sections or dropped. ADR-0011 already defines *how*
external content may enter (inbound proposal via reviewed PR, never a
bidirectional-authoritative merge); what's missing is a *type* for it.

## Decision

1. **`reference`** is a durable Kind under `.nugit/references/`, sharing the
   common front-matter plus a `source:` field (URL / DOI / standard id). The
   body carries the **claims distilled for this project — never the full
   document**; `source` and `provenance.citation` link out.
2. **Retrieval**: references select like lessons (in-scope + task-keyword
   match), sit **below lessons and above glossary** in the truncation priority,
   and support the **`informs:<id>`** edge — declared on the reference itself
   (the decision it grounds is immutable and usually predates it), honored by a
   reverse pass so a reference surfaces whenever the object it informs is in
   the bundle.
3. **Entry path**: an agent (or human) reads the source, distills the
   project-relevant claims, and opens a PR adding the object — the ADR-0011
   inbound-proposal path, reviewed like any knowledge change.
4. Projections (Obsidian index, Notion) include references like other kinds.

## Rejected

- **Squeeze research into existing types** — lessons demand a trigger/incident
  and ADR Context bloats with material that isn't the decision's rationale;
  both types lose their shape and their retrieval semantics.
- **Ingest full documents** — a 4k-token bundle budget makes pasted papers
  self-defeating; distillation is the value, the link preserves fidelity.
- **An external RAG index / vector DB for research** — violates git-native +
  deterministic retrieval; the index stays deferred-with-trigger (ADR-0004).
- **`informed_by:` edges on decisions** — requires mutating immutable, already
  ratified ADRs every time new research lands (contradicts ADR-0003's spirit);
  the reference carrying `informs:` keeps writes append-only.

## Consequences

- "How do I feed research into the project brain?" now has a typed answer with
  provenance, scope, keywords, supersession (research goes stale — supersede,
  don't edit), and budget-bounded retrieval.
- References are the lowest-priority *typed* content: they ground decisions but
  never crowd out the decisions themselves.
- Anti-pattern to watch in review: reference objects used as a dumping ground
  (full abstracts, no scope/keywords). The PR reviewer holds the line.
