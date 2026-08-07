---
schema_version: 1
id: ADR-0038
type: decision
scope: global
status: proposed
created: 2026-08-06T02:20:37Z
relates_to:
  - amends:ADR-0036
  - constrains:adopt
  - constrains:gitutil
  - informs:ADR-0032
  - informs:ADR-0035
  - informs:ADR-0037
  - informs:ADR-0021
provenance:
  commit: seed
  citation: "fix/adopt-cross-repo-paths"
confidence: high
---

# ADR-0038 — A cited path is only this repo's phantom if this repo once had it

## Context

[[0036-adopt-from-docs]] admits a prose token as a claimed unit under three
structural rules, and rule 3 — *path anchor* — is exempt from the two-document
corroboration the other statistical rule requires. The stated reason is sound as
far as it goes: "its evidence is the **tree**, which one document cannot
manufacture."

The tree is not this repo's alone.

Measured read-only against a real repo, of **6** documented-but-absent findings
**5 were false positives, every one of them admitted by `path-anchor`**. All
five are real directories — in a **sibling repo in the same organization**. The
citing document is a decision record whose entire subject is the boundary
between the two repos; its prose says, in the sentence before it names
`apps/<service>` and `libs/<lib>`, that *the other side of that boundary is
first-class*. It is not describing this repo's layout. It is describing the
sibling's, correctly, in a document about the sibling.

`path-anchor` cannot tell those apart, and structurally never could. Its
premise is "a parent directory that really exists here has an empty slot where
the document points" — but **two sibling repos in one org lay their services out
the same way**, so `apps/` exists on both sides and the empty slot is exactly as
consistent with "that is the other repo's service" as with "this repo deleted
it". The rule is not a little imprecise; it is asking a question whose answer it
cannot see. And it bites hardest on precisely the repos nugit's federation work
targets ([[0032-peer-federation]], [[0035-org-hub-and-promotion]]): siblings in
one organization documenting their shared boundary. The better the docs, the
worse the report.

The sixth finding — the true positive — was a service this repo really did
delete. The signal separating it from the other five is deterministic, cheap,
and already sitting in the repository:

- the true positive **was tracked by this repo and removed by a commit**;
- the five false positives were **never in this repo's history at all**.

That is the same principle [[0037-units-are-tracked-code]] just established one
layer down. ADR-0037's defect was "detection asked the filesystem what exists,
when the question is what this repository *versions*". This one is the same
sentence with one word changed: **admission asked the filesystem what is
missing, when the question is what this repository ever *had*.** Ask git; it
already knows.

## Decision

**Narrows [[0036-adopt-from-docs]] clause 4 (rule 3) and clause 6, and spends
[[0032-peer-federation]] at adoption time. Everything else in ADR-0036 stands.**

1. **`path-anchor` no longer asserts a phantom on its own. It needs this repo's
   own history to corroborate.** For a cited path that does not exist now,
   adopt consults `gitutil.DeletedPaths`: a path this repository once tracked
   and later removed is a **high-confidence phantom**, reported with the commit
   that removed it and its date. A path this repository **never contained is not
   asserted as this repo's phantom**, whatever the layout suggests.
   - The admission rule itself is unchanged: `path-anchor` still records *why* a
     token was read as a unit claim. What is narrowed is what the report is
     allowed to *assert* from it. Evidence and assertion are separate, and
     ADR-0036 already keeps the rule on every finding so a reader can audit it.
   - Renames are deliberately not detected (`--no-renames`), so a path moved
     away still answers yes. The question is "did this repository ever contain
     this path", and a rename answers it.
2. **The paths asked about are the ones the prose wrote, plus the slot the name
   would occupy under each directory that already holds a detected unit.** The
   second half is not decoration: the measured true positive is a bare *name* in
   three tables and its directory is never written down, so the spellings alone
   ask git nothing. `apps/<name>` is the same slot ADR-0036's absence veto
   already walks, so asking about it costs nothing new and turns an assertion
   into a citation. Both separator spellings are asked, because a claim is filed
   under a hyphenated key and a repo may spell its directories with underscores.
3. **One batched history call per report, never one per candidate.** Same
   discipline as `gitutil.TrackedFiles` in ADR-0037: the caller has a set of
   paths, so the query takes a set. Bounded by `--max-count`, by a pathspec cap,
   and by rejecting anything that could escape the repo or read as pathspec
   magic — the input comes from prose.
4. **A THIRD BUCKET, clearly labelled: "cited here, but never in this repo".**
   The suppressed findings are not thrown away. They are reported in their own
   section, under a heading that says exactly what they are and explicitly does
   not claim they are this repo's, and they are counted separately in the
   headline and the summary line.
   The measured result is what decides this, per ADR-0036's own standard that a
   section which is entirely noise is worse than no section: on the real repo
   all 5 members of this bucket are **correct and interesting** — every one is a
   genuine cross-repo citation, and the set is exactly the boundary surface
   between the two repos. That is the opposite of the path-disagreement section
   ADR-0036 dropped, which was 7/7 unremarkable. A reader who checks the first
   item here learns something true.
5. **When peers are configured, attribute instead of merely excluding.** If a
   configured peer's checkout ([[0032-peer-federation]] `peers:`,
   [[0035-org-hub-and-promotion]] `org.hub`) contains the cited path, the finding
   reads **"documented here, lives in `<peer>`"** and names the path over there.
   With no peers, the fallback is clause 1 alone: the report can still say where
   the path is *not*, and says so in those words.
   - A **path** the prose wrote is matched against the peer's tree directly; a
     **bare name** is matched only against the peer's own `modelfacts.Units`. A
     bare directory match at a peer's root would attribute on coincidences of
     layout (`docs`, `tools`), and this bucket is only worth having while its
     contents are true.
   - An absent peer is not an error and is reported per-peer, verbatim as
     ADR-0032 clause 3 requires. CI checks out one repo; that is the normal
     state, and the report degrades to clause 1.
6. **A unit this repo deleted AND a peer now has stays a phantom**, carrying the
   peer as extra information. Its prose is stale about this repo either way, and
   that is what this report is for; "moved to `<peer>`" is the useful detail, not
   a reason to stop reporting it.
7. **`internal/adopt` still reads no `.nugit/`** — ADR-0036 clause 1 is intact.
   The CLI resolves peers and passes them in. `nugit adopt` takes them from
   `peers:` in `config.yml` when there is one, and from a repeatable
   **`-peer name=path`** flag otherwise, because adoption happens *before*
   federation is configured and the pre-adoption caller has no config.yml to put
   them in. A config that cannot be read is simply no peers; adopt is a report
   and may not fail on one.
8. **`inventory-colocation` is untouched, and so is every veto.** Its
   corroboration is two independent documents *of this repo* naming the same
   missing unit — a claim this repo makes about itself, not an inference from a
   shared layout — and it survives on evidence the cross-repo failure mode
   cannot manufacture. The history is recorded on such a finding when it agrees
   and changes nothing when it does not. Scalar-only disagreements, the absence
   veto, the quorum currency, the blanket rejects and the two-document rule all
   stand exactly as ADR-0036 left them.

## Rejected

- **Keep `path-anchor` unconditional and accept the noise.** The status quo, and
  it fails ADR-0036's own founding argument: "one false 'documented but absent'
  discredits the entire report", because a reader who checks the first finding
  and sees a typo stops reading. At 5 false positives out of 6 the first finding
  a reader checks is *five times out of six* one of these — and worse than a
  typo, it is a name they recognize as the sibling's, which reads as nugit not
  knowing what repo it is looking at. The one true positive would be invisible
  in that list, exactly as the real phantom was invisible among 395 before the
  precision rule existed.
- **Suppress the never-here findings entirely** rather than bucketing them.
  Seriously considered, because ADR-0036 dropped the path-disagreement section
  on precisely this reasoning, and a bucket that is mostly noise is worse than
  no bucket. Rejected **on the measurement, which came out the other way**: the
  dropped section was 7/7 unremarkable, and this one is 5/5 genuine cross-repo
  citations naming the exact boundary the two repos share. In an org context
  that set is the most interesting thing on the page — it is the input to a
  landscape ([[0034-org-landscape]]) and to a contract ([[0033-cross-repo-contracts]]).
  It is bucketed rather than promoted because it is **not an assertion about this
  repo**, and the heading says so. Had the measurement gone the other way this
  clause would say "suppress"; the rule is the measurement, not the preference.
- **Infer the owning repo from prose — a document that mentions another
  system's name by convention describes that system's paths.** Tempting, cheap,
  and wrong twice. It is unexplainable in exactly the way ADR-0036 refused an
  LLM: the finding's rule would be "the paragraph seemed to be about something
  else", which no reader can audit and no run can reproduce. And it takes its
  evidence from **prose, the artifact this entire report exists to distrust** —
  a document stale enough to name a service that no longer exists is not a
  document whose subject headings can be trusted to scope its paths. Every rule
  in ADR-0036 is structural for this reason; the fix for a rule that read the
  tree too naively is not a rule that reads the prose.
- **Require peers to be configured before `adopt` reports anything.** It would
  give the best answer in every case where it applies, and it does not apply in
  the case that matters: **adoption happens before federation is set up.** The
  primary caller has no `.nugit/` at all (ADR-0036 clause 1, with a test), so it
  has nowhere to write `peers:`, and gating the report on configuration that
  only exists post-adoption inverts the whole verb. The history check needs
  nothing but the repo it is already standing in, which is why it is the floor
  and peers are the improvement on top.
- **Ask the history about every claim, including co-located ones, as a
  requirement.** Rejected: it narrows a rule whose evidence is already
  independent and sufficient, and it would silence real findings — a unit
  deleted before a bounded walk can see it, a unit that never had a directory of
  its own, a service renamed at the same time as its directory. Co-location's
  evidence is that *this repo's own documents*, twice over, describe something
  the tree does not have. That is a self-claim; the cross-repo failure mode does
  not produce it.
- **Report the never-here paths as a `landscape` or `contract` finding.** Right
  idea, wrong verb, wrong phase. `adopt` runs before any store exists and writes
  nothing but candidate lessons (ADR-0036 clauses 1, 9, 10); minting a landscape
  edge out of a heuristic path citation would launder prose into the enforced
  text, which is the one thing ADR-0036 protects against. The bucket is the
  honest form: here are the paths, here is who has them, decide for yourself.
- **Use `git log --follow` or rename detection to trace a moved path.** Rejected
  as strictly worse for this question: rename detection would report a
  moved-away path as `R` rather than `D`, hiding exactly the case ("this repo
  used to have it and now does not") the check exists to find.

## Consequences

- **The measured result.** On the real repo, documented-but-absent falls from
  **6 to 1**. The survivor is the genuine deletion, admitted by
  `inventory-colocation` as before and now **citing the commit that removed
  it** — a finding a reader can open at a sha instead of taking on trust. The
  five cross-repo citations move to the new bucket, and with the sibling
  configured as a peer all five are attributed by name and exact path. On a
  second, much larger monorepo the regression check is clean: **2 before, 2
  after**, both `path-anchor`, and both now carry the deleting commit — the
  narrowing costs that repo nothing and pays it evidence. Undocumented units,
  disagreements, staleness and runbook candidates are unchanged on both.
- **The false-negative this buys, stated plainly:** a path that is genuinely
  this repo's phantom but was **never committed** — a service planned in a
  document and never built, filed under a real parent by one document — is now
  invisible unless a second document corroborates it (rule 2's ordinary path).
  That is a real loss of recall and it is the right trade at these numbers:
  ADR-0036's failure modes are all "one-directional toward silence" and this is
  one more of them. A repo that documents a service it never built has a
  planning-document problem, not an inventory problem.
- **`gitutil` grows `DeletedPaths`**, batched and bounded like `TrackedFiles`.
  The lifecycle and recurrence checks are the obvious next callers: "was this
  path ever here" is a question more than one check asks by proxy today.
- **The federation machinery earns its keep before adoption, not after.**
  ADR-0032 phase 1 was retrieval-only by construction; this is the first place a
  configured peer changes what a *report* says, and it does so without reading
  the peer's store at all — only its tree and its detected units. That keeps
  ADR-0032's guarantee intact: peer knowledge still reaches no enforcement
  verdict, and adopt is not an enforcement verdict either.
- **A new asymmetry to watch.** The report now answers "is this mine?" with
  git and "is this theirs?" with the filesystem, because a peer checkout's
  history is not this repo's to interpret. If peers ever grow a history-aware
  question, it belongs in the peer's own store, not in this one's read of it.
- **The generalizable lesson is narrower than "check git".** ADR-0036 learned
  that a rule whose precision is a property of the repo is not a rule. This one
  adds the sibling case: **a rule whose evidence is the tree is only as scoped as
  the tree is**, and in an organization the tree shape is shared vocabulary.
  Any future rule that reasons from layout has to say which repo's layout it
  means, and only the version-control system can answer that.
