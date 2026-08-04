---
schema_version: 1
id: ADR-0035
type: decision
scope: global
status: proposed
created: 2026-08-04T00:00:00Z
relates_to:
  - amends:ADR-0034
  - amends:ADR-0032
  - constrains:config
  - constrains:knowledge
  - constrains:c4
  - constrains:skillopt
  - informs:ADR-0033
  - informs:ADR-0027
  - informs:ADR-0016
  - informs:ADR-0018
  - informs:ADR-0011
  - informs:ADR-0013
  - informs:ADR-0001
provenance:
  commit: seed
  citation: "feat/org-hub"
confidence: high
---

# ADR-0035 — Organization federation, phase 4: the hub, promotion, and org-wide distribution

## Context

Phases 1–3 made a sibling's knowledge **readable** (ADR-0032), its obligations
**checkable** (ADR-0033), and shared infrastructure **ownable** (ADR-0034). All
three are peer-to-peer, and ADR-0033 said so approvingly: "phase 1 already
removed the need for a hub." At two repos that is true. It stops being true for
three reasons, and each of them is visible in the earlier records themselves.

**O(N²) configuration.** Every repo lists every other repo it wants to read.
Two repos need two entries. Ten repos need ninety, and adding the eleventh means
editing ten existing repos to introduce it. The cost is not the typing — it is
that the list is duplicated per repo with nothing to reconcile the copies, so the
org's federation topology becomes N private, silently divergent opinions about
who exists. A hub makes the shape a star: a new repo configures one peer, and the
repos that care about it configure none.

**No canonical home.** ADR-0034 had to invent an ambiguity rule — local wins,
else the single peer that declares a landscape, else *nothing*, with a doctor
finding naming every claimant. That rule exists precisely because **no peer is
privileged**: picking the first in `peers:` order would make the org's shared
model depend on the reader's private, reorderable list. Failing closed to "no
landscape" was the right call given the inputs, and it is a bad outcome: a repo
whose two peers both ship a landscape gets *less* than it did before. The org
does not actually have two writers for one fact in that case; it has one
canonical store that nugit had no vocabulary to name.

**No path from repo-local to org-wide.** This is the failure the pilot review
actually found. An operational lesson about a shared system was stranded in the
repo that got burned, while the repo that owns the system never saw it. ADR-0032
carries such a lesson across on a task-keyword coincidence and ADR-0034 binds it
by path when the org models the system — but neither gives the lesson a way to
*become the org's*. It stays where it was written. Every phase so far improves
reading; nothing improves publishing, so the corpus that most needs to be shared
is the one with the least reach.

The counter-pressure is real and is recorded in three separate places
(ADR-0032, ADR-0033, ADR-0034 all reject a hub *service*). Nothing here reverses
that. The bet stays git-native: **the hub is a peer with a role, not a new
transport.**

## Decision

1. **`org.hub: <peer-name>` names one already-configured peer as the org's
   canonical store.** Reading it is ADR-0032 verbatim — a local checkout, read
   only, never fetched, never authenticated, one level deep. `Peer.Hub` is
   *derived* at config load from `org.hub` and is not YAML-decodable on the entry
   (`yaml:"-"`), so a peer can never declare itself canonical: the reader
   designates one, in one place.

   The name here IS a peer name, deliberately unlike `org.repo`. ADR-0033 point 3
   kept a peer name out of the party-id space because a party id is a *bilateral*
   fact both repos must spell identically. Which sibling this repo trusts as
   canonical is the opposite kind of fact — unilateral, private, and revisable —
   so the reader's own namespace is exactly where it belongs.

2. **The hub's landscape wins outright. This AMENDS ADR-0034 point 3.** Its
   three-step rule becomes four: local wins; else the **hub** wins, over any
   number of other claimants, with no ambiguity; else exactly one peer wins; else
   nothing is used and every claimant is named. Recording this as `amends:` and
   not `informs:` is what makes retrieval annotate ADR-0034 as partially
   overridden — without the edge a reader is served "two or more peers each
   declaring one → NOTHING is used" as if it still held unconditionally, and acts
   on a guarantee that a hub has retired.

   The old rule's reasoning is not refuted, it is *satisfied*. It refused to break
   the tie because no peer was privileged; a hub is privileged by an explicit act
   of designation in this repo's own config, so the tie was already broken by a
   human and nugit is reading the answer rather than guessing it. With no hub the
   old rule applies unchanged, and a hub that declares no landscape does not
   suppress a single other peer's — an absent file is not a claim.

3. **A hub that is not configured, or not checked out, degrades exactly like any
   absent peer.** Doctor says which of the three it is — none designated, named
   but not in `peers:`, or configured but not checked out — because the
   remediations are unrelated and a single "no hub" would hide two of them.
   Nothing fails, no check reddens, and `pr-render` is unaffected. This is
   ADR-0032 point 3 applied to a role instead of a path.

4. **`nugit promote <id> [-to <peer>] [-force] [-dry-run]` copies a local record
   into the hub's checkout.** It writes `<hub>/.nugit/<kind>s/<file>` and stops.
   **It never commits, never pushes, never touches the network, and never runs
   git inside the hub — not even to read.** The human who owns the hub reviews
   the dirty file and opens the PR there. That boundary is ADR-0011's "the only
   writer into `.nugit/**` is a reviewed PR" carried across a repo boundary, and
   it matters more here than locally: the agent running promote frequently has no
   business landing anything in the hub, and a tool that could would be a tool
   that *would*, on some tired afternoon, at scale.

   The destination is always a **configured peer** (`-to` picks a different one;
   there is no raw-path form), so the set of directories promote can write into
   is exactly the set this repo already declared it federates with.

5. **Provenance is rewritten; the id is not.** The promoted file carries
   `provenance.origin_repo` (the source repo's `org.repo`), `provenance.origin_path`,
   and `provenance.commit` (the source HEAD), so the hub can always answer "where
   did this come from" mechanically rather than by reading a sentence. Promotion
   **refuses without `org.repo`** — a record the hub cannot attribute is worse
   than no record, and ADR-0033 point 3 already settled that nugit never guesses
   an identity.

   The **id is never rewritten** (ADR-0001): it is a stable human key, and the
   record's own prose and `relates_to` still spell it the old way, so renaming it
   would dangle the record against its own graph. The rewrite is a line-level
   front-matter edit, not a YAML re-marshal — the same reason ADR-0016 gave for
   `ratify`: the hub owner's review diff should be "these lines differ from the
   source", not "the whole header was reformatted".

6. **It lands as `status: proposed`.** The candidate lane (ADR-0016) applies at
   the hub too, and the hub owner ratifies. Arriving pre-ratified would mean any
   repo could write directly into another repo's *corpus* — not its candidate
   queue, its corpus — by copying a file. Symmetrically, **an unratified record
   cannot be promoted**: the candidate lane is a local review queue, so exporting
   a draft asks the org to review something the author's own repo has not.
   Superseded and invalidated records are refused too — promoting a dead record
   publishes a known-wrong answer org-wide.

7. **Dedup before writing, using the dedup that already exists.** If the hub
   holds a live record of the same kind whose keywords overlap under the ADR-0018
   rule (≥2 shared keywords covering ≥ half the candidate's set), or whose title
   words do, promote **refuses and names it**, so the org merges knowledge into
   one record instead of accumulating near-duplicates. `-force` overrides.

   The rule is reached through `internal/distill`, not reimplemented: two notions
   of "we already know this" would mean the same pair of lessons is a duplicate
   inside one repo and a novelty at the hub, which is the worst possible place
   for the disagreement to live.

8. **Two refusals are NOT overridable, because they are correctness hazards
   rather than judgement calls.** An id the hub already holds under a different
   record — ADR-0001 keys are stable and are not rewritten, so nugit will not
   mint a second one. And a `supersedes:` edge whose target the hub also holds:
   copying that record in would derive the **hub's** record to superseded, which
   is exactly the silent cross-store kill ADR-0032 point 5 exists to prevent,
   arriving this time through a copy rather than a merge. Other `relates_to`
   targets the hub lacks are *reported*, never refused — a citation that resolves
   nowhere is inert, and the origin stamp is how a reader chases it home.

9. **`nugit export -format skillopt -peers` spans local and peer/hub lessons**,
   so every repo's incidents feed one corpus instead of one benchmark per repo.
   Three properties make it safe:

   - **The ADR-0027 leakage gate applies to a foreign lesson unchanged.** A leaky
     case is permanent and poisons every number derived from it, and nothing
     about a lesson having been written next door makes its trigger less likely
     to state its own answer.
   - **The ADR-0032 admission gate applies too**: global scope, ratified only.
   - **Case identity carries the origin.** A foreign case is
     `lesson:<origin>/<slug>`, because identity in a merged set is `(origin, id)`
     and the case number is precisely where that bites: a consumer hashes it into
     a train/val/test split, so two repos' same-named lessons colliding would put
     one case in two splits and no two measurements would compare.

   **Without the flag, behaviour is byte-identical to today** — including the
   absence of an origin label, which is why the label is added to *every* case in
   federated mode and to none outside it. With one origin the label carries no
   information; adding it unconditionally would change every existing case's
   `labels` and silently invalidate every corpus generated before this existed.
   Nothing reads a peer directory unless `-peers` is passed.

10. **`nugit skill [-install] [-force] [-name]` distributes the agent skill
    files.** `nugit agent -install` already wires MCP, so an adopting repo's
    client learns the `context` tool *exists*; the two skill files are what tell
    an agent when to call it, what a trailer block is for, and that a declared
    architecture edge is not a suggestion. They were distributed by hand-copying
    out of nugit's own checkout — a channel with no version, no update path, and
    no way to tell a stale copy from a current one. They now ship in the binary
    (`go:embed`), with `agent -install`'s exact contract: print by default, write
    under `-install`, never overwrite a differing file without `-force`, and
    never merge (a SKILL.md may carry local edits, and a deterministic tool does
    not rewrite prose it did not author).

    The **embedded tree is canonical**; this repo's own `.claude/skills/**` is
    the installed artifact, and a test pins the two byte-identical. That is
    ADR-0011's outbound-projection shape applied to nugit itself: one writer,
    one derivation, drift detected mechanically rather than noticed eventually.

11. **This also AMENDS ADR-0032, in one narrow, real place**, audited
    deliberately rather than assumed. Point 2 reads: "`knowledge.Load(repoDir)`
    stays local-only, so every WRITER (`ratify`, `reinforce`, `distill`, the
    Notion/Obsidian/**skillopt** projections) keeps seeing exactly this repo's
    store." Two clauses of that sentence stop being true:

    - the **skillopt projection** is named in the list, and under `-peers` it
      reads peers through `LoadWithPeers`. The property the sentence was
      protecting survives — skillopt writes JSONL to stdout and has never written
      into `.nugit/**` — but the sentence as written is now wrong, and a reader
      served it would believe the corpus can only ever contain local lessons.
    - **`promote` is a new writer that reads two stores** (this one and the hub's)
      and writes into the hub's. ADR-0032 could describe writers as local-only
      because there were none that crossed the boundary. There is one now, and
      the discipline that replaces "local-only by construction" is stated
      explicitly in points 4–8: one file, one configured destination, no git, no
      network, `proposed` on arrival, and a hub-side human.

    Recording this as `amends` and not `informs` is what makes retrieval annotate
    ADR-0032 as partially overridden. Everything else in ADR-0032 stands: the
    `(origin, id)` identity rule (point 4) is *relied on* by point 9's case
    numbering; edges-resolve-within-their-own-store (point 5) is what point 8's
    `supersedes` refusal protects across a copy; peer admission (point 6),
    local-outranks-peer (point 7) and qualified display (point 8) are unchanged;
    and point 10's structural guarantee is untouched by this decision — promote
    and export are not in the `pr-render` pipeline at all.

    **Nothing in ADR-0033 is narrowed** — checked deliberately. The contract
    machinery, `org.repo`'s meaning, `contracts.mode` and the obligation check are
    all untouched; a contract can be promoted, and it lands `proposed`, which is
    exactly what ADR-0033 point 9 already requires of a candidate obligation.
    Point 3's separation of party ids from peer names is *reaffirmed* by point 1
    above, not weakened: `org.hub` is a peer name precisely because the fact it
    records is unilateral.

## Rejected

- **A hub SERVICE or API** — a daemon, an org-wide index, a REST endpoint that
  aggregates every repo's store. Rejected, consistent with ADR-0013's rejection
  of centralized telemetry and ADR-0011's git-native bet: a service is state that
  is "as of now" rather than pinnable to a commit, plus an availability
  dependency in the read path of every agent, plus an auth story. A designated
  peer needs none of that — the hub is a directory.

  This is a trade-off, not a law, so the **specific triggers that would reopen
  it** are recorded here rather than left to be re-argued:
  1. an org-wide index too slow to rebuild per clone (the read path is a
     filesystem walk per peer; a hub with tens of thousands of records makes that
     a per-agent-call cost, and a prebuilt index has to live somewhere);
  2. **more than roughly 10–15 repos**, at which point cloning the hub into every
     CI job stops being free and N-clone cost becomes a line item;
  3. a **non-git consumer** — an on-call surface, a dashboard, anything whose
     user is not in a checkout — because "read the file from a clone" is not an
     interface for a pager;
  4. **real-time obligation propagation**, where a contract amended in the hub
     must reach counterparties before their next PR rather than at it.

  None of the four is true today, and each is observable rather than a matter of
  taste. Until one fires, a file in a designated peer is the whole design.

- **`promote` opening the PR itself** (a `gh pr create`, a push, an API call).
  Rejected on three counts, any one of which is sufficient. It needs
  **credentials** with write access to a repo the promoting agent does not own,
  in the read path of a memory tool. It makes the outcome depend on **when it
  ran** rather than on a commit, which is the same objection ADR-0033 raised
  against a counterparty's CI reaching over the network. And it is an **agent
  taking an outward action a human should own**: a PR against another team's repo
  is a request for their attention, and the discipline this tool follows
  everywhere else — distill proposes, ratify is a human act, projections are
  full-overwrite derivations of reviewed text — says the tool writes the file and
  the human sends it. Writing into a working tree is inert until a person acts;
  opening a PR is not.

- **Copying knowledge into every repo instead of one hub** — a vendored
  `.nugit/org/` tree, or `promote` fanning out to all peers. Rejected: **two
  writers for one fact**, which is exactly what ADR-0011 forbids and what
  ADR-0033 and ADR-0034 already rejected for contracts and landscapes
  respectively. The copies diverge on the first amendment, and — worse than drift
  — the repos then disagree about what the org knows, with nothing able to detect
  it. One record, in one place, read by everyone, is the property phase 1 bought
  and this phase must not spend.

  **That argument cuts at promotion too, and the honest position is that
  promotion is a bounded exception rather than an exemption.** After a promotion
  the same text exists in two stores, which is a second copy by any reading of
  ADR-0011. Four things keep it from becoming a second *writer*: the copy is
  **one hop** to **one** designated destination (never fan-out, and the direction
  is fixed — ADR-0032 already rejected the reverse, `promote`-on-import); the two
  are **different objects** under `(origin, id)` identity and can never
  cross-link; the hub's copy is **reviewed on arrival** (`proposed`, ADR-0016) so
  a human decides whether the org adopts it; and **re-promoting an
  already-promoted record is refused outright**, because a copy of a copy is
  where the discipline would actually break.

  What is *not* solved is amendment: change the record at home and the hub's copy
  does not know. That is stated plainly in Consequences, and the fix — a
  supersede-at-origin flow that points the home copy at the hub's — is deferred
  rather than pretended away. Fan-out has the same defect N times over with no
  review step and no single place to fix it; one reviewed hop does not become
  fan-out by degrees.

- **Rewriting ids on promotion** (`ADR-0007` → `consumer-ADR-0007`). Rejected
  twice over, exactly as ADR-0032 rejected it for reads: it breaks ADR-0001's
  stable human keys, and it dangles every reference *inside the record's own
  text* — its prose and its `relates_to` still say `ADR-0007`, so the renamed
  record no longer matches its own graph. The cost of not rewriting is real and
  is accepted with open eyes: two repos that both minted `ADR-0007` cannot both
  promote, and nugit refuses rather than inventing a name. Qualification is a
  display and indexing concern; the bytes are not ours to renumber.

- **Making the hub authoritative for reads** (hub knowledge outranking local, or
  local reads deferring to the hub). Rejected: ADR-0032 point 7's "local always
  outranks peer" is the retrieval ethos — nearest scope wins — and a hub is still
  another repo's checkout. Designation buys the hub tie-breaking authority over
  *other peers*, nothing more.

- **Letting a peer entry declare itself the hub** (`peers: [{name: platform, hub:
  true}]`). Rejected: it puts the designation in the data being read rather than
  in the reader's configuration, so a sibling repo could nominate itself
  canonical for everyone who reads it, and two peers could both claim it — which
  is the ambiguity this decision exists to remove, reintroduced one level down.

- **A `hub:` block with its own path/URL/transport, separate from `peers:`.**
  Rejected: it would be a second way to name another repo, immediately divergent
  from the first, and it invites exactly the network transport the first rejected
  item rules out. A hub being *a peer with a role* is what keeps every ADR-0032
  guarantee — read-only, one level deep, absent-is-normal — true of the hub for
  free.

- **Adding origin labels to every export unconditionally.** Rejected: it would
  change the `labels` of every case in every existing corpus, and a corpus whose
  cases changed shape is not comparable to the measurements taken against it.
  Byte-identity without the flag is a testable property, and it is tested.

## Consequences

- The org gains a promotion route: a lesson learned in one repo can become the
  org's, reviewed by the hub owner, with its origin recorded. The stranded-lesson
  failure that motivated all four phases is now addressable end to end — ADR-0032
  lets the other repo read it, ADR-0034 lets a path summon it, and this lets it
  move to where the whole org reads it.
- **Configuration goes from a mesh to a star for new repos**, but only for new
  ones: nothing migrates existing `peers:` lists, and a repo that wants both
  direct peers and a hub keeps both. The O(N²) argument is about the *growth*
  curve, not about deleting anything today.
- **A promoted record's edges can dangle at the hub.** Its `relates_to` still
  names ids from the origin repo, which the hub may not hold. Reported at
  promotion time, never refused, and resolvable through the origin stamp — but it
  is real, and a hub whose corpus is mostly promoted records will accumulate
  citations that resolve nowhere. The alternative (rewriting ids) is worse; see
  Rejected.
- **Id collisions block promotion outright and there is no automatic fix.** Two
  repos that both minted `ADR-0007` must resolve it by hand at home. Expected to
  be common for ADRs (every repo starts at 0001) and rare for lessons (slug ids).
  A hub adopting this early, while its members' ADR numbering is still small, will
  feel it most.
- **The hub becomes a review workload.** Every promotion lands as `proposed` and
  waits for a human. Doctor reports the pending count so the backlog is visible
  rather than discovered, but nothing bounds it: a hub with no owner is a hub that
  silently fills with candidates nobody ratifies, and those never reach retrieval
  as ratified knowledge (ADR-0032 point 6 admits only ratified foreign objects).
- **Promotion is not idempotent across amendments.** Re-promoting after the origin
  record changes requires `-force`, and nothing tells the hub that its copy is
  stale — the copy rots exactly as ADR-0032 predicted a vendored copy would. What
  is different is that the copy is now the *canonical* one, reviewed and ratified
  at the hub, so the origin's record is the derived one. That inversion is
  deliberate but under-specified: **explicitly deferred** is a supersede-at-origin
  flow that points a promoted record's home copy at the hub.
- **The federated corpus is environment-dependent**, like every other federated
  read: the same commit yields a different benchmark depending on which siblings
  are checked out. Reported per-peer in the export summary and the JSON report,
  and a score is only comparable against a pinned set of checkouts — which was
  already true of a pinned commit (ADR-0027).
- **`nugit skill -install` gives agent instructions a version.** The cost is that
  the skill text now ships in the binary, so improving it means cutting a release
  rather than editing a file — and a repo whose SKILL.md carries local edits will
  see `skipped` forever until someone reconciles it. That is the correct failure
  (never overwrite prose we did not author), and it is silent unless the user
  reads the output.
- **Nothing in the enforcement path moved.** `pr-render`'s findings, the c4↔code
  gate, the significance verdict and the contract check are untouched by
  everything above; a hub can reach a `warn`-severity landscape finding by the
  same route any peer already could (ADR-0033 point 5, ADR-0034 point 6), and no
  further. Promotion writes only outside this repo, and export writes only
  outside the store.
- **Deliberately NOT in this phase**, each its own decision: fetching a hub that
  is not checked out; a hub-side `nugit adopt` that ratifies in bulk; transitive
  hubs (a hub with its own hub); supersede-at-origin after promotion; promoting
  more than one record per invocation; and any org-wide index, service, or
  non-git consumer — see the four triggers in Rejected.
