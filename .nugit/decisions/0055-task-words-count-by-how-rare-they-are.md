---
schema_version: 1
id: ADR-0055
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:retrieval
  - amends:ADR-0043
  - informs:ADR-0024
  - informs:ADR-0006
provenance:
  commit: seed
  citation: "a large pilot repo, 2026-11-21: its 257 replay tasks with oracle keys (nugit export -format tasks, ADR-0044; 838 keys), each run against the store rebuilt at the task's base with the caps lifted to read every key's rank. With the bead title as the task, the shipped ranking served 198 of 838 keys within the caps (23.6%), and the full bead prompt 195 (23.3%); with this change, 343 (40.9%) and 355 (42.4%). Four watch-lesson tasks served the lesson in none of four before, and first in all four after. Prior art: inverse document frequency (Sparck Jones, 1972)"
confidence: high
---

# ADR-0055 — Task words count by how rare they are

## Context

ADR-0043 orders a bundle by relevance tier. Within a tier it uses one bit of
task signal: an item that matches any task word outranks one that matches none.
Ties then fall to scope, status and id.

The bit is almost always set. Task tokens are lowercase alphanumeric runs of at
least three characters, with no stopword list (ADR-0024). A task written in a
sentence contains "the", "for" or "and", and nearly every record contains those
too. So nearly every candidate matches, the bit no longer separates anything,
and within a tier the cap keeps the first items in id order.

On a large pilot repo:

- **Replay tasks.** `nugit export -format tasks` (ADR-0044) mines closed beads
  into tasks: a prompt, the primary file, the base commit, and oracle keys. The
  oracle keys are the records the solution's commits cite or its store changes
  point at, read at base.
  - 257 tasks carry 838 oracle keys.
  - The store was rebuilt at each task's base and the caps were lifted, to read
    each key's rank.
  - 782 of the 838 keys were among the candidates, but only 198 (23.6%) ranked
    within the caps, with the bead title as the task.
  - With the full bead prompt the count was 195. What the task said made no
    difference.
- **Watch lessons.** The task "evaluate Graphify as a code graph for agents"
  matched 119 lessons. The Graphify watch lesson was not among the 8 served. A
  Catena task missed the Catena lesson the same way. A one-word task found
  each one.

## Decision

1. **A task word weighs by how rare it is in the store.** For each task word,
   the weight is ln(N/df). N is the number of objects loaded, local and peer.
   df is the number of those objects whose text contains the word. The text and
   the tokenization are exactly what matching already uses: the body and id, as
   whole tokens (ADR-0024).
   - A word every record contains weighs 0.
   - A word only one record contains weighs the most.
   - An item's weight is the sum over the distinct task words it contains.
2. **The weight orders items within one rank, and does nothing else.**
   - The sort is origin, then relevance rank (tier and miss bit, ADR-0043),
     then weight, highest first, then scope, status and id as before.
   - The weight never moves an item across a tier.
   - It never admits an item past the keyword gate and never cuts one.
   - The caps are unchanged.
   - A task of only ubiquitous words, or no task, orders exactly as before.
3. **One pass answers both "does it match" and "how much".** A single scan of
   the store gives each object its matched words and weight. The global
   keyword gate and the miss bit read it, so the gate, the miss bit and the
   weight cannot disagree about what a match is. The scan also replaces the
   per-candidate tokenization the gate and the miss bit each did. On the
   pilot's store, a call's median went from 0.24 s to 0.20 s.
4. **The weight is never rendered.** The bundle carries an order, not a score
   for a caller to threshold.

On the same replay tasks:

| task given | shipped ranking | this decision |
|---|---|---|
| bead title | 198 of 838 (23.6%) | 343 (40.9%) |
| full bead prompt | 195 (23.3%) | 355 (42.4%) |

- With titles, the tasks that got every oracle key went from 50 to 95 of 257,
  and the mean reciprocal rank of an oracle key from 0.104 to 0.217.
- All four watch-lesson tasks now serve the lesson first: the Graphify and
  Catena tasks above, and two reworded ones. Before, none of the four served
  it at all.

The keys still missed are of two kinds:

- 229 rank below the cap inside their own tier.
- 201 are one-hop pulls, which rank below every direct item, or global records
  beneath a full component tier.

The second kind is tier structure (ADR-0043), and this decision deliberately
leaves it alone.

## Rejected

- **BM25** (term-frequency saturation plus document-length normalization).
  - On titles it served 345 against 343, and on full prompts 375 against 355.
  - It needs two constants, k1 and b, and a constant someone tunes per repo is
    exactly what ADR-0043 rejected.
  - Agents pass short tasks, and that is where BM25 adds nothing.
- **Rarest word first.** Order items lexicographically by the document
  frequencies of their matched words, which needs no floating point. It served
  330 on titles against 343: one rare word outvoted several moderately rare
  ones that together named the subject better.
- **A stopword list.** It fixes "the" and "for", but not "code", "graph" or
  "agents". Those are common in one repo and rare in another, and the rarity
  of a word in this store is the only measure that knows which. A list would
  also make the keyword gate stricter, and drop component-scoped lessons that
  a sentence-shaped task used to admit. Recall on a specific file is the
  failure LESSON-global-scope-gated-on-task-keywords-misses-knowled records.
- **Let the weight cross tiers, or admit by score above a threshold.** A
  declared path, component or citation binding is stronger evidence than word
  overlap (ADR-0043, ADR-0053). A threshold is the rejected "numeric relevance
  score with a threshold" again.
- **Document frequency over the current section's candidates only.** A word's
  weight would then change with the path and the section, so the same task
  could order the same two records differently in two calls. A store-wide
  count is stable and is computed once.
- **An embedding or full-text index.** ADR-0004's index trigger has not fired,
  and retrieval stays deterministic and LLM-free (ADR-0006). The weight is a
  count over the store, so the same store gives the same order.

## Consequences

- A task's distinctive words now decide which records are served first: a
  product name, a component name, an ADR id. A one-line task works best, and
  generic words cost nothing.
- Nearly every call with a task orders its tiers differently. A bundle's set of
  records changes only where the cap cuts.
- ADR-0043's "there is no score" no longer holds literally. What it rejected
  still holds: there is no threshold, no constant to tune, and no score in the
  output. The weight only refines its ordinal ranking.
