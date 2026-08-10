---
schema_version: 1
id: ADR-0039
type: decision
scope: global
status: proposed
created: 2026-08-09T00:00:00Z
relates_to:
  - constrains:knowledge
  - constrains:doctor
  - constrains:consistency
  - constrains:ratify
  - amends:ADR-0001
  - amends:ADR-0022
  - informs:ADR-0032
provenance:
  commit: seed
  citation: fix/duplicate-knowledge-ids
confidence: high
---

# ADR-0039 — An id is unique within a store, and that is now checked

## Context

Two different knowledge objects in one store can carry the same `id:`, and
nothing detects it. Observed on a pilot repo: two distinct decision files,
authored a day apart, both carrying `id: ADR-P-0027` — one `accepted`, one
`proposed`. The collision sat in the store for over a week without a single
surface reporting it.

ADR-0001 made the id the store's stable cross-reference key and said edges point
at keys. It never said two objects may not share one, because nothing about
hand-assigned keys makes that thinkable until it happens. Every consequence is
silent:

1. **`nugit ratify` is undefined.** `Ratify` took the FIRST object matching the
   id and promoted it. With one accepted and one proposed twin it reported "is
   accepted, not proposed" — an error about a file the operator never edited,
   while the real candidate sat unratified. Had the walk ordered the other way
   it would have promoted one file and silently left the other behind, looking
   like success. `ratify -list` showed only the proposed half, so nothing told
   the operator a second file existed.
2. **Retrieval shadows one object with the other.** The `byKey` map and the
   one-hop `relates_to` traversal both index by key; last write wins, so one
   record simply never reaches a bundle.
3. **The edge resolvers are ambiguous.** `ResolveEffectiveStatus`,
   `ResolveAmendedBy` and `ResolveReinforcedBy` all resolve by key: a
   `supersedes:` naming the duplicated id supersedes an arbitrary one of the two.
4. **Every `relates_to:`/`supersedes:`/`amends:` edge pointing at that id has an
   undefined target** — including edges written in good faith years earlier.

`nugit doctor` already had exactly this check for the C4 model
(`model-health`, duplicate element ids — components and containers share one
namespace, last binding wins). The knowledge store, whose ids are far more
widely referenced, had none.

ADR-0032 raises the stakes. Identity across federated stores is `(origin, id)`.
A *within-store* duplicate defeats that pair before a peer is ever involved: the
origin is the same, so the discriminator does no work. The mirror-image mistake
would be worse than the bug — the same id in a PEER store is legitimate and
expected, because every repo mints `ADR-0001`, and a checker that flagged it
would make federation itself read as a defect.

## Decision

1. **An id is unique within one store.** This makes explicit a constraint
   ADR-0001 implied and never stated, and it is the smallest rule that makes
   `(origin, id)` (ADR-0032) actually identify one object.

2. **One shared core, `knowledge.DuplicateIDs`.** A pure grouping over objects
   the caller already loaded — no new I/O — keyed on `(origin, id)`, returning
   every colliding id with **every** file carrying it, sorted. Keying on the
   pair is what makes cross-store reuse structurally unreportable rather than
   merely unreported. Id-less objects are skipped: they are invisible to
   retrieval for a different reason and belong to the untyped check, and
   grouping them on `""` would report every malformed file as a duplicate of
   every other.

3. **`nugit doctor` gains a GATING check**, `knowledge object ids are unique`,
   sitting beside `knowledge objects are typed` and gating for the same reason:
   both are silent data loss. An untyped object vanishes from retrieval; a
   duplicated id makes one object shadow another. The detail names every id and
   every file, **untruncated** — unlike the advisory details around it, because
   the remediation is "open these exact files" and a `… 2 more` tail sends the
   reader searching.

4. **`nugit pr-render` gains a FAIL-severity check**, `duplicate-knowledge-id`,
   scoped to objects the PR adds or modifies, reading the store at the reviewed
   ref like every other store check. Doctor alone would not do: a pre-flight
   nobody runs on a Tuesday is how this collision survived a week. Scoping to
   touched objects is what keeps the blast radius to the PR that can fix it —
   pre-existing drift stays doctor's job, exactly as `prose-supersession` splits
   its two surfaces.

5. **`nugit ratify` refuses ambiguity instead of guessing.** `ratify <id>` on a
   duplicated id errors naming both files and writes nothing; `ratify -list`
   reports the collision as its own block, so the twin that is not a candidate
   is visible rather than filtered away, and exits non-zero because the action
   the listing exists to set up cannot be performed.

6. **Severity is `fail`/gating, not advisory** — and this **amends ADR-0022**,
   which ratified "advisory/warn throughout" for store-lifecycle checks. That
   ruling was reasoned from *precision*: prose matching has irreducible false
   positives (quoted examples, discussion of the mechanism), so warn keeps a
   human in the loop. This check is an exact grouping over committed text with
   **no false positives by construction** — there is no such thing as a
   legitimate within-store duplicate — so the premise that produced `warn` does
   not hold and the conclusion does not carry.

7. **Cross-store id reuse is never reported**, at any severity, on any surface.
   Pinned by tests in `knowledge` and `consistency` that assert the negative by
   name, alongside ADR-0032's existing isolation guards.

## Rejected

- **Advisory in doctor, to avoid reddening an adopter's pre-flight.** The
  counter-argument is real: this can turn an existing adoption red, and the fix
  looks like renumbering, which ADR-0001 discourages. Rejected because the
  premise is false — renumbering a *duplicated* id breaks nothing, since no edge
  naming it resolves correctly today. There is no working reference to preserve;
  that is the whole defect. An advisory check would also have left the pilot's
  collision exactly where it was: reported into a wall of green.
- **Warn (not fail) at PR time, matching ADR-0022's on-ramp.** ADR-0022's
  on-ramp exists to buy precision evidence for heuristics. This check needs no
  such evidence and can gain none — it is set equality. A warn would let the
  collision land, which is precisely what happened.
- **A `duplicate_ids: warn|off` config knob.** Every existing knob (`c4.mode`,
  `contracts.mode`, `recurrence.mode`) exists because the check has a judgement
  call inside it — adoption maturity, or a heuristic's precision. This one does
  not. A knob here would only ever be used to keep a broken store broken.
- **Auto-renaming the newer duplicate.** A writer that mutates a record's key
  without review, dangling every reference in the record's own prose — the
  failure ADR-0032 rejected for foreign ids, for the same reasons, and ADR-0011
  forbids the second writer.
- **Reporting cross-store id reuse, even as info.** Every repo mints
  `ADR-0001`; under ADR-0032 those are two objects under two keys. Surfacing
  that pair would train readers to ignore the check on the one signal that
  matters.
- **Deducting from the store health score.** The gating check already says it,
  loudly. The score is a direction indicator adopters track over time
  (ADR-0022's discipline), and moving its scale for a boolean defect would make
  every historical baseline incomparable for no added information.
- **Deriving uniqueness from filenames instead** (one id per file, id must match
  the filename stem). Rejected: the store deliberately does not couple a record's
  key to its path — `LESSON-<slug>` files, promoted hub copies (ADR-0035) and
  the `SPEC-014-cache.md` filename-id fallback all rely on that decoupling.

## Consequences

- `internal/knowledge` gains `DuplicateIDs`/`DuplicateID`; doctor, consistency
  and ratify consume the one core, so the three surfaces cannot drift apart.
- **`nugit doctor` can now exit non-zero on a store that passed yesterday.** For
  a store with no duplicate the exit code is unchanged, which is every store
  that was ever correct. The remediation is a one-line front-matter edit in one
  file, and doctor names which files.
- `nugit pr-render` gains a `fail`-severity check with an `explain` entry; the
  corpus gains a positive case (`duplicate-knowledge-id-introduced`) and the
  adversarial set a near-miss negative (a shared id *prefix* is two records).
- `nugit ratify -list` exits 1 when the store carries a collision. A script that
  consumed the listing's exit code now learns about the ambiguity instead of
  ratifying into it.
- ADR-0022's "advisory/warn throughout" is narrowed, not overturned: its four
  checks keep their severities. What changes is the reason — severity follows
  from whether a check can be wrong, not from which package it lives in.
- The pilot's remediation is now mechanical: doctor names the id and both files;
  the operator gives one record a free id and `ratify` becomes defined again.
