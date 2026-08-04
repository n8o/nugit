---
schema_version: 1
id: ADR-0034
type: decision
scope: global
status: accepted
created: 2026-08-04T00:00:00Z
relates_to:
  - constrains:c4
  - constrains:consistency
  - constrains:retrieval
  - amends:ADR-0032
  - informs:ADR-0033
  - informs:ADR-0012
  - informs:ADR-0011
  - informs:ADR-0017
  - informs:ADR-0020
provenance:
  commit: seed
  citation: "feat/org-landscape"
confidence: high
---

# ADR-0034 — Organization federation, phase 3: the landscape model (shared infrastructure with a declared owner)

## Context

ADR-0032 made a sibling repo's knowledge readable; ADR-0033 made a two-sided
obligation checkable. Both operate on knowledge *objects*. Reviewing two sibling
repos in one organization surfaced a third gap, and this one is structural:
**the repos share physical infrastructure that neither repo's model can
express.**

- One repo's CI runners and its build service **run on a cluster owned by the
  sibling**. Commits in repo A configure that cluster; one commit in A changed
  scheduling priority *on B's production cluster*. Repo A's model has no way to
  say "the thing I am configuring belongs to B", so nothing in A's review says
  so either.
- A **shared artifact registry**: its configuration lives in repo B, while the
  operational lesson about its retention behaviour lives in repo A, because A is
  the repo that got burned. Both repos independently fought the same retention
  failure for months, each holding one half of the answer. ADR-0032 can carry
  the lesson across, but only on a task-keyword coincidence — nothing connects
  *the file being edited in A* to *the system B owns*.
- A shared build service where a commit in one repo tuned resources for "the
  shared A+B CI workload". Cross-repo resource contention, with no model of the
  contention anywhere.

The shape is the same every time: **a repo can declare its own containers and
components, but nothing above the repo can say "this system is shared, here is
who owns it, and here are the paths that configure it."** ADR-0002 binds files
to components; ADR-0020 binds files to knowledge; neither can bind a file to a
system that belongs to a different repo, because the C4 model nugit parses stops
at the repo boundary by construction.

That boundary is deliberate and must survive. `internal/c4`'s parser treats
`softwareSystem` as transparent — it descends and records nothing — and
`model.Container` says in its own doc that "the parent system is deliberately
not tracked: nugit models a single-system subset, so containers are the top
grouping level." Everything downstream is built on that: `Covered()`'s
two-level roll-up (ADR-0017), `internal/mapping`'s two-level rule table,
`gen-rules`' flattening to go-arch-lint, the C4 delta renderer, and the
c4↔code gate. Making `softwareSystem` first-class would ripple through all of
them to buy a fact that is not even per-repo.

## Decision

1. **The landscape is a NEW, SEPARATE artifact ABOVE the per-repo model:
   `.nugit/architecture/landscape.dsl`.** It is a Structurizr subset parsed at
   the **system level only**, through a distinct entry point
   (`c4.ParseLandscape`) with distinct output types (`model.Landscape`,
   `model.LandscapeSystem`, `model.LandscapeRel`). It reuses the existing
   tolerant lexer and nothing else. `c4.Parse` — the per-repo model parser — is
   **not touched**, keeps treating `softwareSystem` as transparent, and never
   sees this file. Two artifacts, two parsers, two type sets, one shared
   tokenizer.

   ```
   workspace {
     model {
       gateway = softwareSystem "Consumer Gateway" {
         properties { "nugit_repo" "consumer-gateway" }
       }
       registry = softwareSystem "Shared artifact registry" {
         properties {
           "nugit_owner" "producer-service"
           "nugit_paths" "platform/registry/**,deploy/registry/*.yaml"
         }
       }
       gateway -> registry "pulls build artifacts"
     }
   }
   ```

   - `nugit_repo` marks a system as **being** one of the org's repos, named by
     the same stable org-wide id ADR-0033 established as `org.repo`.
   - `nugit_owner` names the repo **accountable** for a shared system. A system
     with an owner is *shared*; a system without one is just a system.
   - `nugit_paths` are doublestar globs **evaluated against whichever repo is
     reading**, in the ADR-0002 path dialect. This is the modelling move the
     whole decision turns on: the cluster is owned by B, but the paths that
     configure it live in A's tree, so the glob cannot belong to either repo's
     model — it belongs to the org's. Invalid globs are dropped and reported,
     the `mapping.InvalidPatterns()` discipline (ADR-0020 point 5), never
     silently swallowed.

2. **The per-repo layer is guaranteed unchanged, and the guarantee is
   mechanical.** `Covered()`, `internal/mapping`, `gen-rules`, the C4 delta and
   its renderer, the c4↔code gate, evidence tiers and the significance verdict
   are all **byte-for-byte unaffected** by anything in this decision. Pinned by
   three tests: `internal/c4/testdata/*_self.golden` stay byte-identical; a
   landscape.dsl fed to `c4.Parse` yields an **empty** model (no components, no
   containers, no relationships, no properties) and an empty `mapping.Mapper`;
   and a repo with no landscape produces byte-identical `pr-render` output to
   one whose landscape exists but is inert. If a future change needs `Covered()`
   or `mapping` to know about the landscape, the layering is wrong — the answer
   is a new artifact, not a wider model.

3. **Exactly one landscape is authoritative for a repo's view (ADR-0011).**
   Resolution is a fixed three-step rule:
   1. a local `.nugit/architecture/landscape.dsl` **always wins**, and when it
      exists no peer landscape is read at all;
   2. otherwise, if **exactly one** configured peer declares one, that is the
      landscape, stamped with the peer's name as its origin;
   3. otherwise — **two or more peers each declaring one** — nothing is used,
      and the ambiguity is surfaced as a doctor finding naming every claimant.

   Silently picking the first peer in `peers:` order would make the org's shared
   model depend on **the reader's private, reorderable peer list** — the exact
   conflation ADR-0033 point 3 rejected when it refused to let a peer name serve
   as a party id. Ambiguity here means the org has two writers for one fact,
   which is an ADR-0011 violation *in the org*, not a tie for nugit to break.
   Failing closed to "no landscape" keeps every check inert, which is the safe
   direction: the cost is a missing warning, not a wrong one.

4. **A new consistency check, `landscape-ownership`, at `warn`.** When the PR
   touches files matching a landscape system's `nugit_paths` and that system's
   `nugit_owner` is **not** this repo's `org.repo`, it warns, naming the system,
   the owner, and the matched paths, and telling the reader to coordinate with
   the owner. **Inert when `org.repo` is unset** — with no identity nugit cannot
   know whether it is the owner, and ADR-0033 point 3 already settled that
   guessing an identity is worse than having none. Warn only, never `fail`: the
   remediation is a conversation with another team, and a check whose first act
   is to redden a build over cross-team coordination does not get adopted (the
   ADR-0016 ramp, same as `contracts.mode`). Changed files come from the code
   delta at the **reviewed ref**, and the local landscape.dsl is read with
   `ShowFile(head, …)` — `LESSON-read-from-reviewed-ref` applies to this check
   exactly as it does to contracts (ADR-0033 point 6). A peer's landscape is
   unavoidably read from its checkout: this repo has no ref that addresses
   another repo's history.

5. **Retrieval surfaces the shared system, and admits the OWNER's knowledge.**
   When the queried path matches a shared system's `nugit_paths`, the bundle
   gains a `landscape` section naming the system, its owner, and where the
   landscape came from; and every object from the **owning peer** is admitted as
   *landscape-bound*, bypassing the task-keyword gate, marked in JSON and
   markdown, and labelled with its origin. This is what makes the registry case
   work: an agent editing registry configuration in repo A finally sees repo B's
   hard-won retention lesson, because the org's own artifact says that file
   configures B's system.

   The bound set is bounded twice over — by the landscape's globs and by the
   single owning origin — which is why the keyword gate can be dropped where
   ADR-0020 kept it for local lessons: a declared, bilateral, path-level
   statement is strictly stronger evidence of relevance than a keyword
   coincidence, and the flood risk that motivated the gate is not present.
   Everything else in the ADR-0032 retrieval discipline is untouched: local
   still outranks peer as the outermost sort dimension, the peer admission rule
   (global + ratified) still applies, the single spec slot is never displaced,
   and every budget cut is still recorded in `Dropped[]`.

6. **This AMENDS ADR-0032** (`relates_to: amends:ADR-0032`, per ADR-0015), in
   two narrow, real places. Recording it as `amends` rather than `informs` is
   what makes retrieval annotate ADR-0032 as partially overridden — without the
   edge a reader is served guarantees that no longer hold in full:

   - **Point 6's last sentence** — "A foreign `applies_to_paths` glob never
     binds: it addresses the peer's checkout, and matching a local path would be
     a coincidence of layout." Foreign knowledge can now be summoned by a path
     match. The reasoning still holds and is *why* the mechanism is built the
     way it is: the glob doing the binding is **not the foreign object's**. It
     lives in the org's landscape and is defined to be evaluated against the
     reading repo, so it is a declaration, not a coincidence. A peer object's
     own `applies_to_paths` still never binds here.
   - **Point 7's parity clause** — "Peer globals are keyword-gated exactly like
     local globals." A landscape-bound object from the owning peer is admitted
     without a keyword match. Ranking is unchanged: local still outranks peer
     unconditionally.
   - **Point 10's structural guarantee**, already narrowed once by ADR-0033
     point 5, is narrowed slightly further: `pr-render` may now read a peer's
     `landscape.dsl` (only when this repo declares none) and that content can
     reach a **warn** finding. It still cannot reach any `fail`-severity check,
     the significance verdict, or the c4↔code gate.

   Nothing in ADR-0033 or ADR-0017 is narrowed — checked deliberately. The
   contract machinery is untouched, and point 2 above is a promise *to* ADR-0017,
   not a change to it.

7. **Doctor reports the landscape advisorily and never gates**: whether one was
   found and from where, how many systems were parsed and how many are shared,
   invalid globs, the ambiguous-multiple-landscape case, and any `nugit_owner`
   or `nugit_repo` naming a repo id that nothing else the reader can see
   declares — a dangling owner is a check that will never fire and a typo that
   nothing else can catch.

8. **`nugit landscape render -format mermaid`** draws systems, ownership, and
   cross-system relationships, following `internal/c4/render.go`'s conventions
   (`graph LR`, deterministic sort, entity-escaped labels). Shared systems get a
   distinct node shape and carry their owner in the label, so "who owns this"
   is readable off the diagram.

## Rejected

- **Making `softwareSystem` first-class in the per-repo model** (a
  `Model.Systems` slice, parented containers, a three-level `Covered()`).
  Rejected as the deepest possible change for a fact that is not per-repo:
  it ripples into `Covered()`'s roll-up rules, `internal/mapping`'s specificity
  table, `gen-rules`' flattening to go-arch-lint (which has no system level any
  more than it has a container level), the C4 delta and its renderer, evidence
  tiers, and the c4↔code gate — every one of which would need new semantics for
  an element that, in a single-repo model, has exactly one instance and means
  nothing. ADR-0017 bought container roll-up with a *known* hole it documented;
  buying a system level would compound it. And it still would not work: the
  fact being modelled is "B owns this and A configures it", which no
  single-repo model can state no matter how many levels it has.
- **Duplicating the landscape into every repo** so each reads a local copy.
  Rejected: two writers for one fact, which is precisely what ADR-0011 forbids
  and what ADR-0033 rejected for contracts. The copies diverge on the first
  change, and — worse than drift — the two repos would then disagree about who
  owns the cluster, with nothing able to detect it. One declaration, read by
  everyone, is the whole point of phase 1. The single-writer rule is also why
  two peers declaring a landscape is an *error to report*, not a merge to
  perform.
- **Inferring ownership from commit history** (whoever touches the paths most,
  or first, owns the system). Rejected: it inverts the evidence — in the
  motivating case repo A commits to B's cluster constantly, so frequency would
  hand A ownership of exactly the system the check exists to protect. It is
  also unexplainable when it mis-fires, drifts with every merge, and would make
  a warning's presence depend on the shape of the last 90 days of history
  rather than on a declared fact. ADR-0033 rejected inferring `org.repo` from
  the remote for the same reason: a wrong guess is worse than no answer.
- **A hub repo or org-level service holding the landscape.** Rejected:
  ADR-0032 and ADR-0013 already settled that a second store that is "as of now"
  rather than pinnable to a commit, plus an availability dependency in every
  agent's read path, is not worth it. A file in one repo, read over `peers:`,
  needs no service.
- **Extending `applies_to_paths` to cross-repo knowledge** instead of
  introducing a landscape. Rejected: it would mean a peer's globs bind against
  this checkout, which ADR-0032 point 6 rejected for the right reason — the
  peer authored those globs for its own tree. The landscape moves the glob to
  the only place it can honestly live: an artifact that belongs to neither repo
  and explicitly declares itself reader-relative.
- **Putting ownership on the existing `workspace.dsl`** (a model-level
  `nugit_owner` property, or owner tags on containers). Rejected: it puts an
  org-level fact in a per-repo file, so it is duplicated per repo (ADR-0011
  again), and it drags the c4↔code gate's inputs into a fact that has nothing
  to do with this repo's import graph. Separate concern, separate artifact.
- **Failing the build on an ownership violation.** Rejected for this phase: the
  remediation is coordination with another team, sometimes in a repo the author
  cannot write to. Warn makes it visible; a repo that wants a hard gate has
  `contracts` (ADR-0033), which is the mechanism designed for enforceable
  two-sided invariants.

## Consequences

- The three motivating gaps become expressible and, for two of them,
  mechanically visible: a PR in A that configures B's cluster says so in its own
  render, and an agent editing the shared registry's configuration in A is
  served B's retention lesson with a `peer` origin label. The build-service
  contention case is now at least *modelled* — the shared system and its owner
  are declared and rendered — even though nothing yet checks contention itself.
- **The per-repo layer gained nothing and lost nothing.** That is the
  point, and it is the property most likely to be eroded by a future change, so
  it is pinned by tests that fail by name.
- Ambiguity fails closed to no landscape. A repo whose two peers both ship one
  gets *less* than it did before (nothing), plus a doctor finding — deliberate:
  the org has to decide who writes the file, and nugit should not paper over
  that with a coin flip.
- `landscape.dsl` is text and drifts like all text: a system's `nugit_paths` can
  name a directory that was renamed, and the ownership check then silently stops
  firing. Doctor reports invalid globs, but a *valid glob matching nothing* is
  indistinguishable from "this PR didn't touch it" — a known blind spot, the
  same one ADR-0033 accepted for a `must` naming a renamed file, and the reason
  ownership is warn-only rather than a gate.
- Reading a peer's landscape at PR time is one extra `os.ReadFile` per
  configured peer, only when this repo declares no landscape of its own, and
  only when `org.repo` is set. Inside ADR-0006's deterministic, LLM-free budget;
  a repo that has not opted in pays literally nothing.
- **Deliberately NOT in this phase**, each its own future decision: cross-repo
  resource-contention checks (modelling the contention, not just the system);
  a landscape delta in `pr-render` (the landscape is not diffed base..head);
  landscape-aware evidence tiers; ownership at container or component
  granularity rather than whole systems; and `nugit landscape` writers of any
  kind — the file is authored by hand or by the ADR-0012 grounded agent, never
  generated.
