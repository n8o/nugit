---
schema_version: 1
id: ADR-0036
type: decision
scope: global
status: proposed
created: 2026-08-04T00:00:00Z
relates_to:
  - constrains:adopt
  - constrains:modelfacts
  - constrains:gitutil
  - informs:ADR-0008
  - informs:ADR-0012
  - informs:ADR-0016
  - informs:ADR-0021
  - informs:ADR-0027
  - informs:ADR-0028
provenance:
  commit: seed
  citation: "feat/adopt-from-docs"
confidence: high
---

# ADR-0036 — `nugit adopt`: compute the adoption argument from the repo's own prose

## Context

[[0008-brownfield-bootstrap]] made a brownfield repo *adoptable* in one command.
It did not make anyone want to. `nugit init` answers "what would the model look
like"; it never answers the question a team actually asks first, which is
"what is wrong with what we have now".

A sibling repo in the same org — a polyglot monorepo, ~1,360 commits, 21
services — has never adopted nugit. It is not undocumented. It has *two*
hand-maintained inventories, and they are both wrong:

- A top-level agent-instruction file **153 commits stale**. It never mentions
  one of the repo's most active services at all, and it still names a
  decommissioned registry product in a table while three sections elsewhere
  explain that the product was replaced.
- An architecture document **1,327 commits stale of 1,363**, which documents a
  service that **does not exist anywhere in the tree**, and whose port table
  disagrees with the agent-instruction file on at least three services.
- **14 runbooks** keyed by symptom (crash-loop, out-of-memory, volume-pending,
  silent outage…): lessons in prose, none retrievable by scope or path.
- A hand-rolled `.claude/rules/` directory doing a ~10-line approximation of
  scoped rules.

Two competing inventories, both wrong, disagreeing with each other and with
`ls`. That is the strongest adoption argument nugit will ever get — and today
making it requires a human to read 20 documents and diff them by hand, which is
exactly the labour that let them rot in the first place. The argument should be
*computed*, from the repo, before anything is installed.

The facts half of [[0012-ai-drafts-model-code-enforces]] is already sitting in
the repo: `modelfacts.Units` ([[0021-model-drift-check]]) is a deterministic
inventory of the buildable/deployable units of a working tree, and it depends on
nothing in `.nugit/`. Diffing that inventory against the *prose* is the same set
operation `model-drift` performs against `workspace.dsl` — with one hard extra
problem. `workspace.dsl` is typed; prose is not. Deciding which of the thousands
of tokens in a docs tree are claims about this repo's units is the whole
engineering problem, and getting it wrong is not a rounding error: **one false
"documented but absent" discredits the entire report**, because a reader who
checks the first finding and discovers it is a typo stops reading the rest.

## Decision

**A new top-level read-only verb, `nugit adopt`, backed by `internal/adopt`.**

1. **It runs BEFORE adoption, and that is a hard constraint.** Nothing in
   `internal/adopt` reads `.nugit/` — not `workspace.dsl`, not the knowledge
   store, not `config.yml`. Its inputs are the code detectors, the repo's
   markdown and `git log`. The primary caller is a repo that has never run
   `nugit init`, and a test asserts a full report out of a tree with no
   `.nugit/` directory at all.
2. **Detected units come from `modelfacts.Units`.** There is no second detector.
   Whatever `model-drift` and `nugit doctor` believe exists is what `adopt`
   believes exists, so the pre-adoption pitch and the post-adoption check can
   never tell two different stories.
3. **The inventory diff is the core, and it reports three sets:**
   *documented but absent* (named in prose, no such unit and no such directory),
   *present but undocumented* (a real unit no prose inventory mentions anywhere),
   and *disagreements* (the same unit given different ports or different paths by
   two documents, reported with both files, both line numbers and both values).
   Prose inventories are `CLAUDE.md`, `AGENTS.md`, `README.md` and `docs/**/*.md`.
4. **THE PRECISION RULE.** A prose token becomes a *claimed unit* only if it
   survives the blanket rejects and then matches one of four admission rules,
   and the rule that admitted it is recorded on every finding — a phantom nobody
   can audit is a rumour.
   - *Blanket rejects*, applied first: fenced code blocks are skipped whole (a
     shell transcript names binaries, flags and hosts); `SCREAMING_SNAKE`
     (an env var normalizes into a perfectly unit-shaped name); version strings;
     filenames with a known extension; URLs and hostnames; `@scope/pkg` package
     coordinates; bare numbers; tokens under three characters; and tokens whose
     every segment is generic documentation vocabulary.
   - *Rule 1, exact match* — the token IS a detected unit's name or directory
     basename. This never mints a phantom; it is how coverage and disagreements
     are computed, and it is the quorum currency for rule 2.
   - *Rule 2, inventory co-location* — the token sits in a **contiguous** group
     (one table column, one unbroken run of list items at one indent, or one
     heading level of one document) in which **at least two entries, and at least
     half of them, are exact matches**. The document's own structure declares the
     group an inventory of this repo's units. Both halves of that threshold are
     load-bearing: see Consequences.
   - *Rule 3, path anchor* — the token is path-shaped, its parent directory
     really exists and really contains a detected unit, and the full path does
     not exist.
   - *Rule 4, family affix* — the token's first or last separator-segment appears
     in the **same position** in at least two detected unit names, using a
     separator at least two units use. Position matters: `-service` trailing two
     real units is a family; the word "service" appearing anywhere in anything is
     documentation vocabulary. A leading English qualifier (`per-`, `multi-`,
     `cross-`, `non-`…) disqualifies the token — that shape is a phrase, not a
     name.
   - *Beyond rule 1, a token must be NAME-SHAPED*: at least two segments, or a
     path. A bare single word is admitted only by exact match.
   - *And nothing is called absent while a directory of that name exists* — at
     the repo root or under any directory that already holds a detected unit.
     This veto is the load-bearing guard, because [[0021-model-drift-check]]
     documents real detector blind spots (no Python or Rust unit detector): a
     real service the detectors cannot see would otherwise be reported as a
     phantom, which is the single most discrediting output this report can
     produce.
5. **The two directions are deliberately asymmetric.** "Documented but absent"
   is computed from a narrow set of inventory slots under the rule above.
   "Present but undocumented" is computed by searching the **full text** of every
   document, token by token, not just its inventory slots — because there the
   costly false positive runs the other way, and calling a documented service
   undocumented is just as discrediting. Precision on both, by opposite means.
6. **Staleness per document**: last-touched commit, its date, and how many
   commits have landed since — via two bounded `git` calls per document
   (`gitutil.LastCommitFor`, `gitutil.CountCommits`, both capped so a scan is
   O(cap) not O(history)), computed for the root inventories always and for any
   document that made a unit claim. The headline is the stalest one, stated as
   "N commits behind, of M in this history", because that ratio is the argument.
7. **Runbook candidates reuse the ADR-0027 gate's symptom lexicon, not a third
   one.** `skillopt.LooksLikeSymptom` is exported for exactly this: it answers
   the cue question the export gate's `trigger-not-a-symptom` signal already
   answers, and nothing else. A document is a candidate if it lives under a
   `runbooks/`-style directory or its title reads as a symptom. Its Trigger comes
   from a Symptom/Trigger/Problem-ish section (matched by keyword, so "Likely
   cause" and "Root cause" both land) or from the title when the title itself
   reads as an observation. **Where either half cannot be extracted
   deterministically, the report says so and names the gap** —
   [[0028-distill-trigger-from-symptom]] already settled that refusing beats
   inventing, and a fabricated symptom is unfalsifiable at review time and
   permanent in the store.
8. **Read-only by default; `-format markdown` (default) and `-format json`.**
   Writing the model, the config and the store is `nugit init`'s job, and adopt
   deliberately does not do it: the whole finding of this report is that prose
   *about* a repo is unreliable, so a verb that reads prose has no business
   writing the enforced text.
9. **One opt-in write: `-write-candidates`** lands the runbook candidates in the
   **candidate lane** (`.nugit/lessons/`, `status: proposed` —
   [[0016-candidate-lane-and-ratify]]), never anywhere else. A candidate whose
   symptom could not be extracted carries `distill.TriggerTODO` verbatim, so the
   gap is visible in review and the lesson is refused by the ADR-0027 export gate
   by construction. Existing files are never clobbered, so a second run over the
   same shelf writes nothing.
10. **Exit 0, always. This is a report, never a gate.** It has no `-fail-on`, it
    is not wired into `pr-render`, and it does not feed the significance verdict.
    A brownfield repo's documentation debt is not its next PR author's fault.

**Nothing in [[0008-brownfield-bootstrap]] is narrowed by this**, so the edge is
`informs:`, not `amends:`. ADR-0008's three clauses — reverse-engineer the model
from the real import graph with the checker's own analyzer, default `c4.mode` to
warn, make `config.yml` load-bearing — all stand unchanged and un-scoped. `adopt`
runs strictly before `init` and writes none of those artifacts; it is the missing
step in front of ADR-0008, not a revision of it.

## Rejected

- **Have an LLM read the docs and emit the model.** It would produce a better
  narrative than any of this, and it is the wrong tool twice over. It is
  unexplainable — a finding with no rule attached cannot be audited, and the
  first false phantom ends the conversation with no way to show why it happened.
  It is unreproducible — the same repo yields a different pitch each run, so a
  team cannot re-run it after a docs cleanup and compare. And
  [[0012-ai-drafts-model-code-enforces]] already puts the agent *behind* a
  grounded step: facts are extracted deterministically, the agent abstracts over
  them, a human ratifies. `adopt` is squarely on the facts side of that line, and
  the nugit-model skill remains the place an agent gets to interpret.
- **Auto-generate a `workspace.dsl` from the prose.** The prose is the thing
  that is wrong. This report exists because two documents describe units that do
  not exist and disagree with each other about the ones that do; a model derived
  from them inherits every one of those errors and launders them into the
  enforced text, where the c4↔code check would then dutifully enforce a phantom
  service. `nugit init` derives from the build graph precisely so the model
  cannot disagree with the code by construction (ADR-0008), and that property is
  the one thing worth protecting here.
- **Make `adopt` a gate** (a `-fail-on`, or a `pr-render` check). Two failures at
  once. It bills the entire accumulated documentation debt of a brownfield repo
  to whoever opens the next PR — the exact mistake ADR-0021 rejected when it
  refused a full-scan warn on every PR — and it would be a gate whose findings
  are heuristic by admission, which trains people to bypass checks before they
  ever trust one. The report's job is to be *convincing*, and a report that
  blocks your build is not read, it is disabled.
- **Report every prose token that is not a detected unit.** The status-quo-by-
  default option and by far the worst. Measured against the real sibling repo
  before the precision rule was tightened, the co-location rule alone emitted
  **395 "documented but absent" names**, nearly all of them English words
  ("full", "new", "origin") admitted because several of that repo's units are
  named after single common words, which makes ordinary prose tables look like
  inventories. Nobody audits 395 findings; the one real phantom in that list would
  have been invisible.
- **Extract runbook triggers with a per-repo heading list.** Real runbooks write
  "Symptom", "Likely cause", "Root cause", "What to try" and "Remediation steps"
  for two slots, and an exact-match list needs a new entry per house style and
  silently yields nothing on the next repo. Keyword matching inside the heading
  is coarser and generalizes; where it finds nothing the candidate is reported
  with its gap named, which is the honest answer.
- **A separate prose-aware detector for units.** Tempting (it would catch the
  ecosystems ADR-0021's inventory misses) and forbidden by ADR-0002's ancestry:
  two inventories that can disagree is the disease being diagnosed here, and
  shipping a second one inside the diagnosis tool would be funny exactly once.

## Consequences

- **The report's own numbers are its validation.** Run read-only against the real
  sibling repo it reports 22 detected units, 19 prose inventories, **1**
  documented-but-absent (the genuine phantom service, confirmed to have no
  directory among the 21 that exist), 9 present-but-undocumented, 3 cross-
  document port disagreements, and 14 runbook candidates of which 4 yield both
  halves deterministically. The stalest inventories date to 1,361 commits ago of
  1,363; the agent-instruction file, 153. Those independently reproduce the
  hand-audit that motivated this ADR, which is the strongest evidence the rule is
  calibrated rather than tuned.
- **Known failure modes of the precision rule, all deliberate, all one-directional
  toward silence:**
  - *A one-word phantom is invisible.* A bare single-word service name
    documented in a repo that has no such directory is admitted by no rule,
    because bare words cost too much. Recall traded for credibility.
  - *The worst documents are the hardest to catch.* Rule 2's half-the-group
    threshold means an inventory that is MOSTLY phantoms fails to qualify as an
    inventory at all. A document that is 60% wrong is invisible where one that is
    20% wrong is caught. Rules 3 and 4 partly cover this; nothing covers it fully.
  - *A repo whose units share no affix and whose docs have no tables gets only
    rules 1 and 3* — the report degrades to staleness plus undocumented units,
    which is still an argument, but a thinner one.
  - *A third-party dependency that shares the family affix* (`foo-client` in a
    repo of `*-client` units) is admitted and reported as absent. The `@scope/pkg`
    reject removes the common case; a bare unscoped one survives.
  - *A renamed unit reads as a phantom.* Correct — the prose IS stale — but a
    reader may score it a false positive.
  - *A monorepo package directory outside any unit-bearing parent* evades the
    directory veto, so a real-but-undetected unit filed somewhere unusual can
    still be reported absent.
- **`internal/adopt` is the first package to depend on `internal/skillopt`
  inbound**, for `LooksLikeSymptom` only. That lexicon is now a shared surface
  across three call sites (the export gate, distill's parity test, adopt), which
  strengthens the case ADR-0028 already made for folding the shared vocabulary
  into one package — still follow-up work, still not blocking.
- **`gitutil` grows `LastCommitFor` and `CountCommits`**, both bounded. They are
  general enough that the recurrence and lifecycle checks may want them.
- **The pitch names what it could not determine.** A repo with no docs, no
  detected units or no git history gets a `Notes` section saying so rather than
  an empty report — a blank pitch reads as "nugit found nothing wrong here",
  which is the opposite of the truth.
- **`-write-candidates` makes ratification a prerequisite**, exactly as ADR-0027
  observed for a freshly-distilled store: the imported runbooks are `proposed`,
  so they rank below ratified knowledge in retrieval and export as nothing until
  someone reads them. On the sibling repo that is 14 lessons to review, 10 of
  them carrying a `TriggerTODO`. That number is the honest measure of how much of
  those runbooks is retrievable prose versus procedure.
- **A team that fixes its docs makes this report shrink**, which is the intended
  incentive and also the intended irony: the fastest way to make `nugit adopt`
  boring is to adopt nugit.
