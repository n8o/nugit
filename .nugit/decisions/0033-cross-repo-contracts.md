---
schema_version: 1
id: ADR-0033
type: decision
scope: global
status: proposed
created: 2026-08-04T00:00:00Z
relates_to:
  - constrains:knowledge
  - constrains:consistency
  - constrains:config
  - informs:ADR-0032
  - informs:ADR-0011
provenance:
  commit: seed
  citation: feat/cross-repo-contracts
confidence: high
---

# ADR-0033 — Organization federation, phase 2: cross-repo contracts and obligation checks

## Context

ADR-0032 made a sibling repo's knowledge **readable**. Reviewing two sibling
repos surfaced the next gap: an obligation that was written down, agreed, and
**never enforced anywhere**.

- A decision in the producer repo states, in its own Consequences, that the
  consumer repo **must add a mirror pin/guard** for the contract to be
  symmetric. The consumer never did. The footgun that ADR documents is still
  armed there — at **three call sites, one of which disagrees with the other
  two**. Both repos are green.
- The same ADR's Rejected section says a cross-repo CI check was **deferred
  because "CI cannot reliably read the sibling repo"**. That premise is now
  false: ADR-0032 gives exactly that read access. The feature request is written
  inside the record it blocks.
- The producer repo then **re-tripped the same class of bug a second time**, in
  a different component, *after* documenting it. Prose in one repo did not stop
  a recurrence in the repo that wrote the prose.
- A second instance of the identical shape: another ADR says the sibling "must
  implement the check-in endpoint to the contract", tracked only as prose.

The pattern is a **two-sided invariant recorded in one repo's prose, with
nothing in the other repo's CI that can fail when its half is missing**. Every
existing nugit check is single-repo by construction: stale-knowledge,
decision-coverage, spec-linkage, and c4↔code all compare this repo against
itself. A half-implemented cross-repo contract is invisible to all of them, and
visibility (ADR-0032) is not enforcement — a human still has to read the
sibling's Consequences paragraph and notice a sentence about themselves.

The key structural insight is that **phase 1 already removed the need for a
hub**. Once a repo can read its peers' stores, the repo that owns the invariant
can declare it once, and each counterparty reads it and checks *its own* half.
No central index, no promotion, no landscape model, no service.

## Decision

1. **`contract` is a sixth durable Kind**, alongside lesson / decision / spec /
   glossary / reference, under `.nugit/contracts/` — the ADR-0014 precedent for
   adding a type. Its front matter carries `parties:`, a list of `{repo, must}`:

   ```yaml
   id: CONTRACT-0001
   type: contract
   scope: global
   status: accepted
   parties:
     - repo: producer-service
       must:
         - name: transport version pinned in the shared env file
           file: third_party/versions.env
           matches: '^TRANSPORT_TAG=v0\.3\.'
     - repo: consumer-gateway
       must:
         - name: mirror guard passes the standard protocol list
           file: apps/gateway/src/server.cpp
           matches: 'useStandardProtocols'
   ```

   `repo` is a **stable org-wide repo id**. A `must` is `{name, file, matches}`
   plus optional `absent: true` for "this must NOT appear". `file` is
   git-root-relative in the party's own repo (the ADR-0002 path dialect).

2. **`matches` is a Go RE2 regexp and nothing else.** RE2 has no backtracking,
   so matching is **linear in the input** — an adversarial or merely clumsy
   pattern from another repo cannot burn CI, which a PCRE-style engine could.
   A `must` whose `file` does not resolve at the reviewed ref is **UNMET, not an
   error**; so is a malformed pattern and a `must` missing `file` or `matches`.
   An assertion nobody can evaluate has not been satisfied, and the finding says
   which of those it was.

3. **The repo declares its own identity: `org.repo:` in `.nugit/config.yml`.**
   This is deliberately **not** the peer name. A `peers:` entry's `name` is the
   *reader's* private label — the display namespace this repo chose for a
   sibling (`platform:ADR-0020`), which the sibling never sees and can change
   at will. A party id is a **bilateral fact**: both repos must spell it the
   same way or the contract silently binds nobody. Conflating them would make a
   contract's meaning depend on a label the counterparty picked in private.
   **With no `org.repo`, contract checking is inert** — it never guesses from
   the directory name, the git remote, or the module path. Fail closed to doing
   nothing, never to guessing which party you are.

4. **A new consistency check, `contract-obligation`.** It gathers contracts from
   the local store *and from peers*, selects the party whose `repo` equals
   `org.repo`, and evaluates that party's `must` list. Other parties'
   obligations are **ignored**: they are not this repo's business and this
   repo's CI is not the place they can be fixed.

5. **Peer admission extends to contracts.** ADR-0032 admits only global +
   ratified decision / lesson / reference from a peer. `contract` joins that
   list — a contract's entire purpose is to be read by the counterparty, so
   excluding it would make the type useless across the boundary. The rest of the
   ADR-0032 gate is unchanged: global scope only, ratified only.

6. **Assertions are evaluated at the reviewed ref, never the working tree.**
   `LESSON-read-from-reviewed-ref` and ADR-0029's `LoadAtRef` pattern apply
   verbatim: existence comes from `ListTree(head)` and content from
   `ShowFile(head, path)`. A dirty checkout, a CI merge commit, or a local edit
   must not change the verdict. The *contract record* from a peer is
   unavoidably read from the peer's checkout as-is — this repo has no ref that
   addresses another repo's history — but every file the contract asserts
   **about this repo** is ref-addressed.

7. **Severity is `warn` by default**, configurable via `contracts.mode:
   warn|fail|off`; an unknown value falls back to `warn`. This is ADR-0016's
   candidate-lane discipline and the same ramp the spec-linkage and recurrence
   checks took: a check whose first act is to redden a build that was green
   yesterday — over a file in *another* repo's ADR — does not get adopted. Note
   the asymmetry with the other enum knobs: `c4.mode` and `-fail-on` fail closed
   to *strict* because their strict state is the repo's own declared policy;
   `contracts.mode` falls back to the **default** (like `capture.commit_msg`),
   because a typo must not silently hand another repo the power to fail this
   repo's CI.

8. **A finding is actionable without opening the contract.** It names the
   contract by qualified id, its origin (`local` or `peer <name>`), the record's
   path, the party id it matched, the unmet `must` **by its `name`**, the file,
   the pattern, and why it failed.

9. **Only ratified contracts fire.** A `proposed` contract is a candidate
   (ADR-0016) — a draft obligation is not an obligation. Superseded and
   invalidated ones are dead. Same rule for local and foreign contracts, though
   a foreign one is already filtered by the ADR-0032 peer gate.

10. **Identity is `(origin, id)`, exactly as ADR-0032 requires.** A peer's
    `CONTRACT-0001` and a local `CONTRACT-0001` are two different contracts,
    reported separately, rendered as `producer:CONTRACT-0001` and
    `CONTRACT-0001`. The obligation index is keyed on the pair, never the id.

11. **Retrieval surfaces contracts that name this repo**, labelled with origin,
    inside the existing budget/`Dropped[]` discipline. They fill after the
    single spec slot and before decisions: a contract naming this repo is an
    **obligation on this code**, not advice about it, and the admitted set is
    bounded by "contracts that named us". It never displaces the spec.

12. **Doctor reports contracts advisorily** — how many name this repo and how
    many of their obligations are currently unmet — and never gates.

## Rejected

- **Executing a script, shell command, or arbitrary program as an assertion**
  (`run: ./check-mirror.sh`, `command:`, a plugin hook). Rejected as a **hard
  security boundary, not a trade-off**: a contract is text this repo reads from
  *another repo's checkout*, and running it would mean an edit in a sibling repo
  — or anything that can write into a sibling checkout — executes with this
  repo's CI credentials on every PR. There is no sandbox, allow-list, or
  review-gate design that makes "code from over there runs here" acceptable for
  a memory tool. A declarative regexp over a named file is strictly less
  expressive on purpose. **`must` will never grow an execution verb.**
- **The counterparty's CI reaching into this repo over the network at check
  time** (an API call, a webhook, a status check posted cross-repo). Rejected:
  it needs credentials, availability, and an auth story in the read path of
  every PR; it makes the verdict depend on when it ran rather than on a commit;
  and it inverts the ownership — the repo that must fix a violation should be
  the repo whose build goes yellow. ADR-0032 already established that two local
  paths and a filesystem read need no service (ADR-0013, ADR-0011).
- **Duplicating the contract into both repos**, so each checks a local copy.
  Rejected: two writers for one fact, which is precisely what ADR-0011 forbids.
  The copies diverge on the first amendment, and — worse than drift — the two
  halves would then disagree about what was agreed, with nothing able to detect
  it. One declaration, read by both, is the whole point of phase 1.
- **`fail` severity by default.** Rejected: the first repo to adopt this would
  discover its unmet obligations as a red build caused by a sentence in a
  sibling repo's ADR, with the fix in code it may not own yet. Warn makes the
  gap visible and lets a team promote to `fail` once its half is landed — the
  same ramp `c4.mode: warn` gives model adoption.
- **A central hub repo / org-level contract registry.** Rejected *for this
  phase* (see Consequences): phase 1 removed the need. The repo that owns the
  invariant declares it; counterparties already read it. A hub adds a third
  place to write, an availability dependency, and a promotion workflow before
  anything has proven it needs one.
- **Checking and reporting the OTHER party's obligations here.** Rejected for
  this phase: this repo cannot verify a claim about a checkout that may not be
  present, has no ref to pin it to, and cannot fix it. Reporting "the sibling is
  in breach" from a PR that touches neither the sibling nor the contract is
  noise pointed at the wrong build.
- **Inferring `org.repo` from the git remote URL, directory name, or module
  path.** Rejected: a guess that is wrong is worse than no identity at all — it
  silently binds this repo to *someone else's* obligations, and CI checkouts,
  forks, and mirrors all make the guess unreliable. Absent identity is inert and
  says so in doctor.
- **Glob or literal `contains:` instead of a regexp.** Rejected: the motivating
  assertions need anchoring and alternation (`^TRANSPORT_TAG=v0\.3\.`); a
  substring test would match the same string inside a comment or a changelog
  entry. RE2 gives the precision with a linear-time guarantee.

## Consequences

- The motivating gap is now mechanically detectable: the producer declares the
  contract once, and the consumer's own `pr-render` says, in its own build,
  which obligation of its own is unmet, in which file. The deferral reason
  recorded in the pilot's ADR ("CI cannot reliably read the sibling repo") is
  retired by ADR-0032 plus this.
- **ADR-0032's point 10 is narrowed, deliberately.** Peer knowledge was
  retrieval-and-context only and could reach no finding. It now can — but only
  through three explicit gates that all have to be opened by *this* repo:
  `peers:` configured, `org.repo` configured, and a ratified contract that names
  this repo by that id. With any one absent the check is inert, and the default
  severity is `warn`. A peer still cannot reach the c4↔code gate, the
  significance verdict, or any other check.
- **Deliberately NOT in this phase**, each its own future decision: a central
  hub repo or org-level contract registry; `nugit promote` (adopting a peer
  record as local); a landscape / system-of-systems C4 model spanning repos;
  reporting other parties' obligations informationally; contracts that assert
  over structure (an AST, a model element) rather than file text; and fetching a
  peer that is not checked out.
- A contract is text, so it can drift from the code it describes exactly like
  any other prose — a `must` can name a file that was renamed. That drift is
  now *visible* (UNMET with "file not found") rather than silent, which is a
  strict improvement over prose, but it is a new maintenance surface: renaming a
  guarded file in either repo requires superseding the contract.
- `contract` enters every place that enumerates kinds (projections, ratify,
  doctor's store health). Ratification of a contract uses the ordinary
  candidate-lane flow, so a two-sided invariant gets reviewed before it can fail
  anyone's build.
- Retrieval gains a section that can consume budget ahead of decisions. Bounded
  by the number of contracts naming this repo — expected to be single digits —
  and every drop is still recorded in `Dropped[]`.
- The check runs at most one `ListTree` plus one `ShowFile` per distinct
  asserted file per PR, and is skipped entirely without identity or contracts.
  It stays inside ADR-0006's deterministic, LLM-free budget.
