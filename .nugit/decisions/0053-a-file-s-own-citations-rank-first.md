---
schema_version: 1
id: ADR-0053
type: decision
scope: global
status: proposed
created: 2026-11-20T00:00:00Z
relates_to:
  - constrains:retrieval
  - constrains:usage
  - amends:ADR-0043
  - informs:ADR-0020
  - informs:ADR-0024
provenance:
  commit: seed
  citation: "a large pilot repo, 2026-11-20: 1,712 code files name a decision by id; on a random 60 of them, context(file, budget 8000) served 19 of the 135 decisions those files cite (14%) and every cited decision for 2 files; with this change, 127 of 135 (94%) and 58 files, and on 400 files 790 of 801 (98.6%). The misses are files citing more than the 12-decision cap and one superseded decision. Prior art: Graphify (Graphify-Labs/graphify) turns ADR/RFC citations in comments into graph nodes, for Python and JS/TS only, with a pattern that misses namespaced ids"
confidence: high
---

# ADR-0053 — A file's own citations rank first in its bundle

## Context

Code in a repo that uses nugit names its decisions. The comment that says
`// DRAWING IS THE GPU'S (ADR-JBS-0091)` was written by the author of the code
beside it, pointing at the record that governs it. It is the most specific
relevance signal a bundle could have, and `context()` ignores it.

On a large pilot repo 1,712 code files cite a decision by id. On a random 60
of them, `context(file)` at budget 8000 served 19 of the 135 decisions those
files cite (14%). Only 2 files got every decision they cite. A typical miss:
a header that cites two global decisions got a bundle filled with twelve other
global decisions in id order, and 88 more omitted. Its two were among the
omitted. nugit's own `internal/retrieval/relevance.go` cites ADR-0043, and its
bundle omitted ADR-0043.

ADR-0043's tiers explain it. A global decision ranks in the global tier, and
inside a tier the order is task match, then scope, status, and id. Without a
task, the lowest ids win. With a task the order barely changes, because whole
token overlap on ordinary words ("file", "path", "rank") matches nearly every
body. A decision scoped to another component is not admitted at all, even when
the file names it. A decision about the MXL flow identity, scoped to the
operator, is cited by the C++ writer it constrains, and that writer never sees it.

The citation is already in the file the agent asked about, so reading it costs
nothing new. The fix needs no index, no store write and no capture step.

## Decision

1. **Read the queried file's citations at read time.** When the queried path
   is a regular file inside the repo, scan its first MiB for identifier-shaped
   tokens. Keep those that are the exact, case-sensitive id of a **local**
   object, and also match each prefix of a token at a hyphen boundary
   (`ADR-0043-style` cites ADR-0043). A directory, a missing file, an
   unreadable file or a path outside the repo has no citations, and its bundle
   is unchanged. A peer's object is never cited: a bare id in this repo's file
   names this repo's record (ADR-0032).
2. **A citation admits the object whatever its scope and whatever the task.**
   Like a path binding (ADR-0020) it substitutes for scope. Unlike one, it also
   substitutes for the task keyword gate on global decisions, lessons and
   references. The author's own pointer is stronger evidence than any keyword
   overlap. The spec slot admits a cited spec the same way.
3. **Cited is a new top tier**, above the path binding. The order is now: cited
   by the file > path binding > component > container > global > one-hop. The
   relevance cap still applies. A file that cites more than twelve decisions
   gets the twelve that rank first among them, and the omission notice says how
   many more there are.
4. **The marker rides every line.** A cited item carries `cited: true` in JSON
   and "cited by this file" in its rendered label, so no reader wonders why a
   decision from another component leads the bundle.
5. **A cited record that is no longer live is named, not served.** A
   superseded or invalidated record is never live context (ADR-0043). The file
   still points readers at it, so the bundle's `cited_dead` and a notice name
   it with its successor. The agent then follows the successor and fixes the
   stale citation when it touches the file.
6. **The usage record counts cited items** (`cited`), so the telemetry review
   can see how often the code's own pointer was the signal (ADR-0013).

## Rejected

- **Ask authors to add `applies_to_paths` for every citing file.** The
  citation already says it, in the file, and it moves with the code. A glob in
  the record has to be kept in step by hand across 1,700 files, and it rots the
  first time a file is renamed.
- **Derive bindings at capture time and write them into the store.**
  Retrieval stays a pure projection (ADR-0024). A derived write goes stale the
  moment the file changes, while a read-time scan cannot.
- **Rank cited items with path-bound items in one tier.** Inside the tier, id
  order would again decide, and a path glob written elsewhere would outrank the
  author's own pointer whenever its id sorts first.
- **Adopt a code-graph tool (Graphify) for citation links.** It links
  citations only for Python and JS/TS, its pattern misses namespaced ids like
  `ADR-JBS-0091`, and it brings a 175 MB derived graph that has to be rebuilt
  on every commit. ADR-0004's index trigger has not fired: this is a reach gap
  that a regexp over one file closes.
- **Raise the decision cap.** On the pilot, every slot was taken by globals in
  id order. More slots would only add more of them.

## Consequences

- The bundle for a file that cites its decisions starts with those decisions,
  however they are scoped.
- A file citing many decisions can push path-bound and component items under
  the cap. That is the intended order, and the cap notice makes it visible.
- A stale citation becomes visible on every read of the file that carries it.
- Scanning costs one bounded read of a file the agent is about to edit anyway.
