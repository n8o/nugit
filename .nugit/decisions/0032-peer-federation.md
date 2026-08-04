---
schema_version: 1
id: ADR-0032
type: decision
scope: global
status: proposed
created: 2026-08-04T00:00:00Z
relates_to:
  - constrains:knowledge
  - constrains:retrieval
  - constrains:config
  - constrains:evidence
  - informs:ADR-0001
  - informs:ADR-0011
provenance:
  commit: seed
  citation: feat/peer-federation
confidence: high
---

# ADR-0032 — Organization federation, phase 1: read-only peer stores

## Context

nugit's unit of memory is the repo. Reviewing two sibling repos in one
organization surfaced knowledge that **exists, is correct, and cannot be
reached from where it is needed**:

- A decision in repo A states, in its own Consequences, that **repo B must add
  a mirror guard** for the contract to be symmetric. Repo B never did, and the
  trap is still armed there. Nothing in repo B has ever seen that sentence. The
  same decision's Rejected section says a cross-repo check was **deferred
  because "CI cannot reliably read the sibling repo"** — the feature request,
  written by the pilot, verbatim, inside the record it blocks.
- A lesson about a **shared registry** lives in repo A because repo A got
  burned. The registry's *configuration* lives in repo B. Both repos
  independently fought the same retention failure for months, each holding one
  half of the answer.
- Diagnostic lessons for a **transport protocol spanning both repos** are
  single-sided. An agent debugging the consumer has no access to the
  producer-side lesson that says "look at the publisher" — the one sentence
  that ends the session.

The seed corpus already exists: **~62% of the pilot store is `scope: global`** —
knowledge its own author declared repo-wide rather than component-bound. That is
precisely the subset whose meaning does not depend on a local component id.

The naive fix — merge two `.nugit/` trees — is silently wrong. Every repo mints
`ADR-0001`; `scope:` is a bare C4 element id matched by **string equality**;
`supersedes:`/`amends:`/`relates_to:` are bare keys (ADR-0001, deliberately).
Concatenating two stores cross-links them: a peer's `supersedes: ADR-0007`
kills the LOCAL ADR-0007, and the local record stops being served as live
context with no error anywhere. That failure is invisible — which is what makes
it the thing this decision is actually about.

## Decision

1. **`peers:` in `.nugit/config.yml`, local paths only.** Each entry is a
   `name` (short, unique, `[a-z0-9-]` — the display namespace) and a `path` (a
   local checkout, relative to the nugit root). Phase 1 never fetches, never
   clones, never authenticates. A malformed `peers:` block, or one bad entry
   inside a good block, fails **closed to "no peers"** and never errors the
   rest of config.yml — federation is additive context and may not become a way
   to disable enforcement by typo.

2. **One peer-aware entry point, one level deep.** `knowledge.Load(repoDir)`
   stays local-only, so every WRITER (`ratify`, `reinforce`, `distill`, the
   Notion/Obsidian/skillopt projections) keeps seeing exactly this repo's store
   — ADR-0011's single-writer discipline is preserved by construction.
   `knowledge.LoadWithPeers` is the additive read path. **A peer's own `peers:`
   is never read.** That is also the cycle guard: A → B → A terminates because
   B's peers are not followed, so no visited-set is needed and no configuration
   can produce unbounded load.

3. **An absent peer is not an error.** CI normally checks out only the repo
   under review, so "the sibling isn't here" is the *normal* state. A missing or
   unreadable peer path degrades to "that peer contributed nothing", is
   reported (doctor, the bundle's `peers` field, the markdown footer), and
   **never fails `pr-render`**. Only the local load can error.

4. **Identity in a merged set is `(origin, id)`, never `id`.** Every foreign
   object carries its peer name as `Origin`, stamped at load. The on-disk id is
   **never rewritten** (ADR-0001: ids are stable human keys, and rewriting one
   would mutate a record we do not own and dangle the peer's own references).
   Every index that keyed on a bare id is now keyed on the pair: the retrieval
   `byKey` map and `pulled` set, `ResolveEffectiveStatus`, `ResolveAmendedBy`,
   `ResolveReinforcedBy`, and `ProseOnlySupersessions`.

5. **Edges resolve only within their own store.** `supersedes`, `amends`,
   `reinforces`, and the one-hop `relates_to` traversal resolve an edge against
   the **source's** origin: a peer's `supersedes: ADR-0007` names the *peer's*
   ADR-0007 and can never touch ours. Enforced twice on purpose — each store's
   derivations run before the merge, AND the resolvers key on the pair, so the
   guarantee survives a future caller that hands a merged slice straight in.

6. **Peer candidates are global + ratified + decision/lesson/reference.**
   A peer's *component-scoped* knowledge is meaningless here: `scope: transport`
   in the sibling and `transport` in this model are unrelated strings that
   happen to match, and scope is compared by equality — admitting them would
   mean silently binding another repo's records to our components. Only what its
   author declared **repo-wide** is safely repo-agnostic. Unratified foreign
   objects stay out: the candidate lane is a *local* review queue (ADR-0016) and
   nobody here can ratify someone else's draft. The single spec slot and the
   glossary stay local — a peer's spec is not the active spec for a path here.
   A foreign `applies_to_paths` glob never binds: it addresses the peer's
   checkout, and matching a local path would be a coincidence of layout.

7. **Peer globals are keyword-gated exactly like local globals**, and **local
   always outranks peer** — origin is the outermost `sortItems` dimension,
   ahead of scope and status. Truncation fills local decisions → lessons →
   references first, then peer decisions → lessons → references; a peer item is
   dropped before any local item, and each drop appears in `Dropped[]`
   qualified, like every other cut.

8. **Foreign ids display qualified** (`platform:ADR-0020`) everywhere a bundle
   or finding names them, with the origin spelled out (`peer platform`) on the
   line. A reader must never mistake peer knowledge for local knowledge.

9. **A foreign object caps at the `declared` evidence tier**, whatever tier it
   holds at home. Every tier above `declared` is a claim about *this* repo's
   substrate — that the components it governs are path-bound here and that this
   repo's edge checks fail on violations. None of that is true of a peer's
   record. Re-displaying its own `enforced` would be a lie about who is
   checking.

10. **Peer knowledge is retrieval-and-context only.** It may not affect any
    `fail`-severity check, the significance classification, or the c4↔code
    gate. Structurally guaranteed: the `pr-render` pipeline reads the store at
    the reviewed **ref** and does not consult peers at all.

## Rejected

- **A central service, index, or daemon that aggregates the org's stores.**
  Rejected: it contradicts ADR-0013's rejection of centralized telemetry and the
  whole git-native bet (ADR-0011) — a second store that is "as of now" rather
  than pinnable to a commit, plus an availability dependency in the read path of
  every agent. Two local paths and a filesystem read need no service.
- **Rewriting foreign ids on import** (`ADR-0001` → `platform-ADR-0001`).
  Rejected twice over: it breaks ADR-0001's stable human keys, and it dangles
  every reference *inside the peer's own text* — its prose and its `relates_to`
  still say `ADR-0001`, so the renamed record no longer matches its own graph.
  Qualification is a **display and indexing** concern; the bytes on disk are not
  ours to change.
- **Copying peer knowledge into the local store** (a vendored `.nugit/peers/`
  tree, or `promote` on import). Rejected: two writers for one fact, which is
  exactly what ADR-0011 forbids. The copy rots the moment the peer supersedes
  the original, and nothing here would ever learn that it had.
- **Loading peers transitively.** Rejected: it needs a cycle guard, makes the
  reachable corpus depend on configuration in repos this one cannot see, and
  turns a bounded local read into an unbounded graph walk. One level is
  explainable — "the repos I named" — and one level covers the org-sibling case
  that motivated this.
- **Admitting component-scoped peer knowledge under a name-mapping table.**
  Rejected for phase 1: it makes federation depend on a hand-maintained
  cross-repo mapping that nothing validates — the drift surface ADR-0021 exists
  to fight, re-introduced across a boundary neither repo can check.
- **Failing (or warning at `fail` severity) when a peer is unreachable.**
  Rejected: it would redden CI on every run, since CI checks out one repo. A
  feature whose default state is a red build does not get adopted.

## Consequences

- Foreign context is now reachable, labeled, and ranked below local. The three
  motivating gaps are addressable: the mirror-guard obligation, the shared
  registry, and the single-sided protocol lessons all live in `scope: global`
  records.
- **Deliberately NOT in this phase**, and each is its own decision: a hub repo
  or org-level store; `promote` (adopting a peer record as local); cross-repo
  obligation checks (the mirror-guard *enforcement*, as opposed to its
  visibility); a landscape / system-of-systems C4 model spanning repos;
  git-fetching a peer that is not checked out; and any write to a peer.
- Retrieval now reads a second tree from disk. Cost is one filesystem walk per
  configured peer per call, bounded by `.nugit/` size — within ADR-0006's
  deterministic, LLM-free budget, and skipped entirely when no peer is
  configured or checked out.
- Bundle composition becomes environment-dependent: the same commit yields
  different context depending on whether the sibling is checked out. That is
  accepted (and reported per-peer) because the alternative is failing on
  absence, and because peer content never reaches an enforcement verdict.
- The `(origin, id)` keying is now load-bearing across four packages. It is
  pinned by tests that assert the *negative* — a peer's `supersedes`/`amends`/
  `reinforces`/`relates_to` must not touch a same-id local object — and those
  tests fail loudly, by name, when isolation breaks.
- A peer name enters the display namespace (`platform:ADR-0020`). Renaming a
  peer changes every rendered id from it; nothing persists the qualified form,
  so no stored data breaks.
