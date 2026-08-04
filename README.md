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
   `.nugit/plan.yml`.

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

**External research has a typed home** — `.nugit/references/` holds *distilled*
external sources (papers, vendor docs, standards, benchmarks): common front-matter
plus `source:`, scoped + keyworded for retrieval, linked to the decisions they
ground via `informs:<id>` edges (ADR-0014). Distill the claims, link the document —
never paste the paper.

**The store fills itself** — `nugit init` installs a commit-msg hook that validates
trailer blocks; `nugit remember` jots ephemeral working memory; `nugit distill`
promotes deliberate `decision:`/recurring `learned:` trailers into durable ADRs/lessons.

## Quickstart

```sh
go build -o nugit ./cmd/nugit

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
./nugit explain c4'<->'code                                     # finding rationale
```

Exit code is non-zero when a finding reaches `-fail-on` severity (default `fail`),
so it drops straight into CI.

### Wiring your agent

Capture works out of the box, but retrieval stays dark until your coding agent
knows how to launch `nugit mcp`. `nugit agent` prints (or installs) the exact
per-client MCP config:

```sh
nugit agent -client claude-code -install   # writes project-scoped .mcp.json (never merges; -force overwrites)
nugit agent -client cursor                 # snippet for ~/.cursor/mcp.json (absolute repo path baked in)
nugit agent -client codex                  # TOML snippet for ~/.codex/config.toml
nugit agent -client opencode               # snippet for opencode.json in the project
nugit agent -client generic                # plain mcpServers JSON for any MCP-aware client
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
