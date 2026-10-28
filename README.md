# nugit

> Git-native typed knowledge + a unified PR view. Every PR shows how the
> architecture is changing, what the code is changing, what the project's
> knowledge is changing, where this sits in the plan, and **why** — computed from
> typed artifacts that live in the same git history as the code.

All five roadmap phases are shipped and adversarially reviewed ([ROADMAP.md](ROADMAP.md)):
any-codebase adoption, **four-language** architecture enforcement, the `context()`
retrieval half of the thesis, the self-filling capture lifecycle, and presentation.

## What works today

**The unified PR view** (`nugit pr-render`) computes, deterministically and with no
external datastore:

1. **C4 delta** — structural diff of `.nugit/architecture/workspace.dsl`.
2. **Code delta** — `git diff` grouped by C4 component via `properties { paths }`.
3. **Knowledge delta** — added/changed/superseded decisions, specs, lessons,
   references, with each decision's `Rejected` rationale.
4. **Plan position** — live from a **Beads** store (`.beads/*.jsonl`), degrading to
   `.nugit/plan.yml`. Scoped to **the plans this PR moved**, not the repo's whole
   board (ADR-0040) — see [The plan store](#the-plan-store).

…then runs **cross-artifact consistency checks** that make the view *verify* rather
than present:

- **c4↔code / cmake↔code / python↔code / ts↔code** (`fail`) — code introduced a
  cross-component dependency the model doesn't declare. Real dependency graphs:
  Go (`go/parser`), C++ (CMake `target_link_libraries`), Python (imports),
  TypeScript (dependency-cruiser).
- **stale-knowledge** (`warn`) — a PR touches code governed by a
  `superseded`/`invalidated` object without updating it.
- **decision-coverage** (`warn`) — an architectural change with no ADR or
  `decision:` trailer.
- **spec-linkage** / **capture-hygiene** (`warn`) — a commit claims a missing spec,
  or a trailer block is missing a mandatory field.

…and gates disclosure by **significance** (trivial / feature / architectural). Run
`nugit explain <check>` for any finding's rationale + remediation.

**Agent memory** — `nugit context -path <file>` returns a scoped, typed,
budget-bounded knowledge bundle (architecture slice + in-scope decisions/spec/lessons
+ references + glossary + ephemeral notes); `nugit mcp` serves it as an MCP tool so
Claude Code can call it. Every served call is appended to a local, gitignored usage
log (`.nugit/.cache/usage.jsonl`; opt out with `usage: {log: off}`), and `nugit stats`
aggregates it — calls by source, top components, truncation rate, plus the two gap
signals: unresolved paths (model coverage) and empty bundles (capture coverage).
Records carry the Claude Code session id (`CLAUDE_CODE_SESSION_ID`) and, under
agent-deck, `AGENTDECK_INSTANCE_ID`. The optional pre-edit hook (`nugit agent
-client claude-code -install -hook`) adds **edit coverage**: every Edit/Write is
recorded with whether a `context()` call for that component or path preceded it,
and `nugit stats` reports the coverage percentage per session. `context_gate:
warn` (default) tells the agent to call `context` first; `block` refuses the edit
until it has; `off` records nothing (ADR-0042).

A bundle is filled by relevance, not to the budget: exact-path bindings, then the
component, then its container, then global, then one-hop pulls, task matches first
within each tier, and at most 12 decisions / 8 lessons / 6 references (override under
`retrieval: {max_decisions, max_lessons, max_references}`). The MCP result is also
hard-capped at 40,000 characters, whatever `budget_tokens` the agent passes, because
Claude Code puts a larger result in a file the model never reads. A clamp or an
omission is stated in the bundle's `notices` (ADR-0043).

**External research has a typed home** — `.nugit/references/` holds *distilled*
external sources (papers, vendor docs, standards, benchmarks): common front-matter
plus `source:`, scoped + keyworded for retrieval, linked to the decisions they
ground via `informs:<id>` edges (ADR-0014). Distill the claims, link the document —
never paste the paper.

**Two-sided invariants are enforceable across repos** — `.nugit/contracts/` holds
`type: contract` objects: an invariant spanning two repos in one organization,
declared **once** by the repo that owns it, naming each party by a stable org-wide
repo id. Each counterparty reads it through `peers:` (ADR-0032), matches its own
`org.repo` identity, and checks **only its own** obligations at the reviewed ref —
so "the sibling must add a mirror guard" stops being a sentence nobody's CI can
fail on (ADR-0033). Assertions are declarative only: a Go RE2 regexp (linear time)
over one named file, optionally negated with `absent: true`. A contract can never
execute a script or a command — it is text read out of another repo's checkout.
Warn by default (`contracts.mode: warn|fail|off`), and entirely inert until you
configure `org.repo`: nugit never guesses which party a repo is.

### The plan store

`.beads/**/*.jsonl` is the plan the repo is executing **now** — one `epic` per
step, written by [`bd`](https://github.com/gastownhall/beads), committed, and
read by `pr-render` at the reviewed ref. It is deliberately **not** a mirror of
GitHub Issues: the backlog stays in the issue tracker and a bead exists because
someone is working that step this week. A mirror rots (nothing forces it true); a
store you must close out to finish a PR does not.

`bd` keeps one database **per repository**, so on a repo running several agent
sessions every plan any of them is executing lands in the same store. nugit
groups the store into **plans** and shows a PR only the ones it moved:

| resolved from | when |
|---|---|
| a `plan` field on the line | always wins |
| the store **file's** name | the store spans more than one `.jsonl` |
| the **id family** — id minus its last segment, for ids with 3+ segments | otherwise |

So `acme-rift-16` and `acme-rift-14` are one plan; `acme-118` (a `bd`-native
`<prefix>-<n>` id) is its own. A PR that closes `acme-rift-16` renders `acme-rift`
and names any other plan with a step in flight — it never presents another
agent's progress as its own. `plan.scope: all` opts back into the whole board.

```bash
nugit plan check                     # lint the store the way pr-render reads it
nugit plan normalize -write          # one stable line per bead → git can merge it
nugit plan normalize -split -write   # one file per plan → agents stop overlapping
nugit plan normalize -split -merge HEAD -merge-base <last-export> -write
                                     # …and a database behind HEAD cannot revert it
```

**Why normalize.** `bd export` serializes the whole database in the database's
own order, so two agents who closed two *different* steps still produce two
whole-file rewrites of the same path — a conflict git cannot resolve and a
hand-merge invents states no `bd` command produced. Normalizing gives every bead
one stable line (sorted by plan, then natural id, keys sorted, every field `bd`
writes preserved byte-exact), so those two agents produce **disjoint hunks that
merge by themselves**. `-split` goes further and gives each plan its own file, so
they never touch the same path at all. Both are `bd`-safe: only the
serialization is rewritten, never a status or a value. Run it after `bd export`,
before `git add`.

**Why merge.** `bd` keeps one local database per clone, and nothing pulls other
sessions' merged plan changes into it, but `bd export` writes *every* plan from
it. Exporting from a database that is behind main therefore reopens other
agents' closes and deletes plans added since the last import. `-merge <ref>`
folds the store committed at `<ref>` back in. Given `-merge-base`, the previous
raw export of the same database, it is an exact three-way merge per bead and per
field (no timestamps needed; `bd` moves none for a label or dependency edit), and
deleting a bead in `bd` carries through. Without a base it falls back to
`updated_at` and keeps any committed close. Derived dependency counts are
recomputed. Save each export as the next run's base (ADR-0047).

**Why check.** The reader is deliberately tolerant — it skips an unparseable
line, drops a duplicate id last-write-wins, renders an unknown status as
`remaining`, and hides non-epics behind a footnote. Every one of those is a step
that silently renders as something other than what its author wrote, so
`plan check` reports them, and pr-render raises the same ones as `plan-store`
findings (`plan.mode: warn|fail|off`).

**The store is `bd`'s, not nugit's** — nugit reads it and rewrites only its
serialization. Close a step when it is *done*, not when the enabling groundwork
lands, and move the bead **in the same PR that does the work**: a step closed in
a later PR is a step nobody can tell was ever finished.

### Approving what the store proposed

Distilled and drafted records land `status: proposed` — a candidate lane
(ADR-0016) a human promotes. `nugit review` is where that happens:

```bash
nugit review              # serves the lane on loopback, opens a browser
nugit review -no-open     # over SSH: print the URL instead
```

One card per candidate, oldest first, opened to its Context, Decision and
**Rejected** — Rejected at the same weight, because it is the field that stops
an alternative being re-proposed and the reason the record is worth reading.
**Ratify** grants the status that record's type earns (`accepted` for a
decision, `active` for a lesson) by calling the same code `nugit ratify` does;
**Withdraw** deletes the candidate, and git history keeps it. `j`/`k`/`a`/`x`
if you'd rather not reach for the mouse.

It writes the working tree and **commits nothing** — it names the files it
touched so you can read the diff and commit it yourself. It is not a service:
loopback only, started by hand, no state of its own, nothing in the delta path,
and every mutating route needs the token in the URL the command prints. A queue
covers the repo it was launched in, since a peer's records are read-only by
design (ADR-0032).

The lane is worth a surface because it rots without one: 24 records sat proposed
across two stores when this shipped, the oldest 100 days (ADR-0051).

**The store fills itself** — `nugit init` installs a commit-msg hook that validates
trailer blocks; `nugit remember` jots ephemeral working memory; `nugit distill`
promotes deliberate `decision:`/recurring `learned:` trailers into durable ADRs/lessons.

## Quickstart

```sh
go build -o nugit ./cmd/nugit

# BEFORE adopting anything: what does this repo's prose claim, and is it true?
# Read-only, works with no .nugit/ at all, always exits 0 (ADR-0036).
./nugit adopt                      # the pitch: phantom services, undocumented units,
                                   # documents that disagree, and how stale each one is
./nugit adopt -format json         # the same report, machine-readable
./nugit adopt -peer platform=../platform   # a sibling checkout: cited paths that live
                                   # over there are attributed, not called phantoms
./nugit adopt -write-candidates    # …and import the runbook shelf as proposed lessons

# adopt in ANY repo: scaffold .nugit/ and bootstrap a C4 model from the real
# dependency graph (Go import graph / CMake / Python / TS — auto-detected), so the
# first render is green, not a wall of failures. Installs a commit-msg hook too.
./nugit init                       # warn mode by default; -mode enforce when ratified
./nugit init -layout cmake         # force a backend: cmake | python | ts | container | flat

# render the PR view for the current branch vs a base
./nugit pr-render -C . -base main -head HEAD                # markdown (default)
./nugit pr-render -base main -head HEAD -format check-run   # GitHub Checks JSON
./nugit pr-render -base main -head HEAD -format json        # structured deltas for agents

# agent memory
./nugit context -path internal/foo/bar.go -task "add caching"   # scoped knowledge bundle
./nugit mcp                                                      # MCP stdio server
./nugit stats -since 168h                                        # is retrieval being used? (local log)

# capture + presentation
./nugit remember -text "watch out for X" -scope foo             # ephemeral working memory
./nugit distill -base main -head HEAD                           # promote trailers → ADRs/lessons
./nugit c4 render | ./nugit c4 gen-rules                        # Mermaid / go-arch-lint config
./nugit c4 preview                                             # live C4 diagrams via local Structurizr renderer (Docker)
./nugit export -format skillopt > cases.jsonl                   # lessons → eval cases (leakage-gated; report on stderr)
./nugit export -format skillopt -peers > cases.jsonl             # …spanning peer/hub stores too (origin-labelled)
./nugit explain c4'<->'code                                     # finding rationale

# organization federation (ADR-0032…0035)
./nugit promote LESSON-foo                                      # copy a ratified record into the org hub's checkout
./nugit promote LESSON-foo -dry-run                             # …show what would land, and where
./nugit skill -install                                          # write the agent skill files this binary ships
```

Exit code is non-zero when a finding reaches `-fail-on` severity (default `fail`),
so it drops straight into CI.

### Wiring your agent

Capture works out of the box, but retrieval stays dark until your coding agent
knows how to launch `nugit mcp`. `nugit agent` prints (or installs) the exact
per-client MCP config:

```sh
nugit agent -client claude-code -install   # writes project-scoped .mcp.json (never merges; -force overwrites)
nugit agent -client claude-code -install -hook  # …and merges the pre-edit hook into .claude/settings.json
nugit agent -client cursor                 # snippet for ~/.cursor/mcp.json (absolute repo path baked in)
nugit agent -client codex                  # TOML snippet for ~/.codex/config.toml
nugit agent -client opencode               # snippet for opencode.json in the project
nugit agent -client generic                # plain mcpServers JSON for any MCP-aware client
nugit skill -install                       # writes .claude/skills/**/SKILL.md (never merges; -force overwrites)
```

`-install` supports claude-code only, because its `.mcp.json` is project-scoped;
the user-global configs (cursor, codex) are printed with the resolved absolute
repo path so you can merge them by hand. Use `-bin /path/to/nugit` if the binary
isn't on the client's `PATH`.

### In CI (composite Action)

```yaml
# .github/workflows/nugit.yml
on: { pull_request: { types: [opened, synchronize, reopened] } }
permissions: { contents: read, pull-requests: write }
jobs:
  pr-view:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: n8o/nugit@v0.3.0      # builds nugit, renders, sticky PR comment + gate
        with: { fail-on: fail }
```

## nugit models itself

`.nugit/architecture/workspace.dsl` is a C4 model of nugit's own packages, with
each component bound to its source via `properties { paths }`. The c4↔code check
therefore validates nugit's own import graph — the "bootstrapping spike" from the
plan. Add an import that violates the declared architecture and `nugit pr-render`
fails the check naming the offending edge.

## Layout

```
cmd/nugit/            CLI entrypoint
internal/
  model/              shared types (no internal deps — the contract)
  gitutil/            git plumbing
  trailers/           commit-trailer parser
  goimports/          Go import analysis (the spike)
  c4/                 Structurizr-DSL-subset parser + structural diff
  mapping/            file→component glob resolver
  knowledge/          .nugit/** reader + supersede-graph status
  delta/              the four deterministic deltas
  consistency/        cross-artifact checks
  significance/       disclosure-tier heuristic
  render/             markdown / check-run / JSON
  engine/             orchestration: two refs in, a Report out
.nugit/               nugit's OWN knowledge (model, decisions, spec, glossary)
PLAN.md · ROADMAP.md  the re-shaped plan; ADRs live in .nugit/decisions/
```

## Design decisions

Key decisions (and the review findings they resolve) live as real nugit
knowledge objects under [`.nugit/decisions/`](.nugit/decisions/). Start with
[ADR-0004](.nugit/decisions/0004-thin-keystone-first.md) (why the keystone ships
first) and [ADR-0002](.nugit/decisions/0002-file-to-component-binding.md) (the
file→component primitive).

## Install & upgrade

```sh
go install github.com/n8o/nugit/cmd/nugit@latest    # or @v0.3.0 to pin a release
nugit version                                        # confirm what you have
```

**Upgrade** by re-running the same command (`@latest` or a newer tag) — it
overwrites the binary in place, so an existing MCP wiring (`.mcp.json` points at
the binary) and the `nugit` skill keep working; just restart your editor so it
relaunches the MCP server. In CI, bump the Action pin (`uses: n8o/nugit@v0.3.0` →
the new tag). `.nugit/` stores are plain git text and forward-compatible within a
schema version. Ensure `~/go/bin` is on `PATH`.

## Status & license

Early — the thin keystone is built, tested, and dogfooded on this repo. The
deferred stages (fitness-function backend, retrieval index, content-addressing)
are conditional; see [PLAN.md](PLAN.md). Licensed under [MIT](LICENSE).
