---
schema_version: 1
id: ADR-0052
type: decision
scope: render
status: accepted
created: 2026-11-08T00:00:00Z
relates_to:
  - amends:ADR-0016
  - constrains:render
  - constrains:knowledge
provenance:
  commit: seed
  citation: "the pilot's 16 records at status: proposed, read one by one on 2026-11-08: most were open product and pricing proposals merged on purpose; at least one recorded a decision its owner had already made"
confidence: high
---

# ADR-0052 — Proposed is a state, not a queue; the PR view says what merging leaves

## Context

A review of the pilot proposed that merging a PR should ratify the knowledge it
adds, to clear the 16 records waiting at `status: proposed`. Reading those 16
showed the premise was wrong for most of them: pricing, product boundaries and
licensing proposals that were merged as proposals on purpose, so the record of
the option exists while the decision is still open. At least one other was the
opposite case: a decision its owner had already made (the record's own
Rejected section says so), written as `proposed` by the agent that captured it.

The PR view showed an added record as one line (id, scope, status) plus its
Rejected text. A reviewer merging it could not see what it decided, or that
merging would leave it undecided.

## Decision

1. **`proposed` keeps its meaning after merge.** A merged proposal is an open
   option on record; it governs nothing until ratified (ADR-0016). Merging does
   not ratify.
2. **The PR view shows what each added or changed record decides**: the first
   paragraph of its Decision section (a lesson's Insight or Rule), extracted at
   parse time as `Gist`, beside Rejected.
3. **An added proposed record carries a note**: it stays proposed after merge,
   governs nothing until ratified, and the exact `nugit ratify` command.
4. **Agents choose the status deliberately.** The skill says: a decision the
   user made, or one the shipped change implements, is written `accepted`
   (`active` for a lesson); `proposed` is only for an option still open.

## Rejected

- **Merge ratifies (opt-in `ratify: merge`).** It would turn every merged open
  proposal into a decision nobody made, and leave no way to record an option
  without adopting it.
- **Derive accepted from "on the default branch".** The same error without even
  a config switch, and the 16 existing proposals would flip at once.
- **A bulk "ratify the backlog" PR.** Already rejected in ADR-0051: the unit of
  judgement is the record.

## Consequences

- `model.KnowledgeObject.Gist` and `knowledge.GistSection` are new.
- Records already merged as proposed stay so until someone ratifies them
  (`nugit ratify`, or `nugit review`); the ones that record a decision already
  made are worth ratifying individually.
