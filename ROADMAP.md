# nugit — roadmap to "truly feature-complete"

Where we are: the **Go-targeted unified PR view** is built, hardened, and validated
(K1/K2/A1 in [PLAN.md](PLAN.md)). This roadmap closes the gap to the *full*
[architecture vision](nugit-architecture.md) — every-codebase reach, the retrieval
half of the thesis, and the self-filling knowledge store.

Designed via a multi-agent pass (5 independent epic designs + synthesis, with
feasibility research). Five epics, ~**4–6 weeks**, dependency-ordered.

## Definition of "truly feature-complete"

1. **Adopts on any codebase** — polyglot + monorepo (a module/subtree nested inside
   a larger git repo) works; the language-neutral PR view runs everywhere.
2. **Architecture enforcement for Go + TypeScript + Python + C++** — C++ via
   CMake's dependency graph (`target_link_libraries`), not the language.
3. **`context(path)` retrieval** — a deterministic, scoped, bounded knowledge bundle
   exposed as an MCP tool (the "agents forget across sessions" half of the thesis).
4. **The store fills itself** — commit-msg hook + ephemeral working memory + a
   distiller that promotes durable knowledge through the PR.
5. **Production presentation** — C4 render + gen-rules, finding IDs + `explain`,
   optional LLM narrative, optional Beads plan, a reusable Action.
6. Deferred items stay deferred *with named triggers* (not silently cancelled);
   `go test ./...` green throughout.

## C++: enforced via CMake, not the language

The language-level objection (C++ has no module system; `#include` graphs are a
preprocessor mess) is real — but **CMake is the dependency system**, and it's
queryable. `target_link_libraries(foo PRIVATE bar)` is an explicit "component foo
depends on bar" edge at exactly C4-component altitude. Two routes, both real:

- **Static `target_link_libraries` parse** — zero configure, zero build env.
  Proven on JBS: **122 components, 204 edges** (`nmos_node`, `st2110_common`,
  `media_transport` as the core libs). Approximate (misses variable/conditional/
  function-defined edges).
- **CMake File API** (you're on cmake 4.2) — exact targets + sources + link deps
  from a *configured* build dir. Accurate; resolves the edges static parse can't
  (JBS has 1,816 `target_link_libraries` calls). Cost: needs a configured build
  tree (a project that already builds in CI has one — reuse it, no fresh configure).

Honest residual: it's **target/module-level** (not catching a rogue `#include`
*inside* an already-linked dependency — which is the right altitude for C4 anyway);
the accurate path needs a build dir; and **non-CMake C++** (raw Make/Bazel) would
fall back to the static parse or warn-only.

## Honest constraints (what we will NOT build, and why)

- **LLM narrative** is descoped to last and opt-in (the deterministic line already
  conveys the facts).
- **Index / merge-driver / `nugit compact` / multi-root orchestration** stay
  deferred-with-trigger per ADR-0004 (retrieval ships in-memory first; the DB only
  lands when "grep is slow" actually fires).

## Phases

### P1 — Any-repo + monorepo support  ·  `MONO-1`  ·  ~L  ·  **unblocks JBS now**
Two changes: (a) **git-root-relative coordinates everywhere** so a nugit root that
isn't the git root stops silently orphaning every path (the JBS nested-module bug);
(b) a **language-agnostic structural bootstrap** — `nugit init` discovers components
from the directory layout (one per `apps/*`, `libs/*`, … via a `-layout` heuristic),
emitting components + globs with **no edges**, so any repo gets the full PR view.
- **Gate:** on a polyglot/nested repo, `pr-render` groups real components (non-empty
  `ByComp`), reads the nested `config.yml`, runs the 4 language-neutral checks, and
  `c4<->code` stays *honestly silent* (never falsely green); empty-prefix path is
  byte-identical to today.
- New ADR: single git-root-relative coordinate system; structural mode is
  intentionally edges-free; honesty rule for un-analyzable edges.

### P2 — Multi-language enforcement  ·  `I1-MULTILANG` (+ `c4 gen-rules`)  ·  ~L/XL
Refactor `goimports` behind one **`Analyzer` interface** (changed-file → internal
dep dirs) consumed by both `consistency` and `bootstrap`; add backends:
- **TypeScript/JS** — dependency-cruiser (MIT; emits resolved-path JSON that drops
  straight into `mapping.ResolveDir`).
- **Python** — stdlib-AST walk (no install needed).
- **C++ — CMake** — static `target_link_libraries` parse (zero-config, proven on
  JBS) with the File API as the accurate upgrade when a configured build dir is
  present. Components = CMake targets, edges = link deps.

Add `nugit c4 gen-rules` (model → go-arch-lint config) as the generator half.
- **Gate:** TS, Python, and a CMake C++ fixture each bootstrap green, then an
  undeclared cross-component dependency is flagged; a missing analyzer binary (or
  no CMake build dir) emits one *info* finding (never a silent pass); all backends
  resolve via the same `ResolveDir`.

### P3 — Retrieval `context(path)` + MCP  ·  `R1`  ·  ~L  ·  **2nd half of the thesis**
The `§8.2` composer: scoped (package→root inheritance, nearer-scope wins), typed,
bounded (truncate by type priority **with a truncation signal** — no silent cuts),
one-hop `relates_to` traversal, read from the git ref. Index is **in-memory first**
(no DB). Exposed as a `nugit mcp` stdio tool so Claude Code can call it.
- **Gate:** bundle ≤ `budget_tokens` with `Dropped[]` populated; nearer-scope wins;
  rebuild-from-git is byte-identical; a real Claude Code session calls the tool.

### P4 — Capture lifecycle  ·  `CAPTURE-LIFECYCLE`  ·  ~L  ·  **fills the store**
`nugit init` installs a **commit-msg hook** (warns/blocks on missing mandatory
trailer fields); **`.nugit-local/`** per-agent ephemeral memory (gitignored);
**`nugit distill`** promotes architecturally-significant / recurring trailers into
durable MADR ADRs + lessons (stable human keys, Rejected section, correct edges),
written into the PR so capture survives squash-merge (ADR-0005).
- **Gate:** a significant trailer promotes to a linked ADR; a recurring one to a
  lesson; a one-off does not; everything round-trips through `knowledge.ParseObject`;
  deterministic/LLM-free.

### P5 — Presentation & integration polish  ·  `PRES-1`  ·  ~M+M  ·  nice-to-have
Finding **IDs + `nugit explain`** (observability); **`c4 render`** (Mermaid block in
the PR comment); **Beads** plan adapter (degrades to the `plan.yml` stub when `bd`
absent); **opt-in cached LLM narrative** (gated to architectural tier, never on the
deterministic path); composite GitHub **Action**; docs.
- **Gate:** narrative-off output byte-identical to today; Beads-absent unchanged;
  every finding has a stable ID + `explain`.

## Critical path & sequencing

```
MONO-1 ─▶ I1-MULTILANG ─▶ R1 ─▶ CAPTURE-LIFECYCLE
              │
              └─ gen-rules (P2)         PRES-1: render+IDs (P2-ish) · Beads+narrative (P5, last)
```

- **MONO-1 is the unblock and the foundation** — it makes paths consistent, which
  everything downstream (multi-lang ResolveDir, retrieval scoping) depends on.
- Full index/vector store, merge driver, `nugit compact`, multi-root orchestration:
  **deferred-with-trigger** (ADR-0004).

## Biggest risks

- **Falsely-green edges** — a structural/partial model must never read as
  "architecture validated." (Mitigation: explicit render note + honesty ADR in P1.)
- **Empty-prefix regression** — the repoDir==git-root path must stay byte-identical.
- **I1 slipping L→XL** — per-language backend quirks (TS perf at scale, Python venv).
- **Distiller promotion policy** — significance/recurrence thresholds need tuning.

## Open decisions for you

1. **C++ default:** zero-config static `target_link_libraries` parse (works today,
   ~204 edges on JBS), or the File API when a build dir is present (accurate,
   resolves all 1,816 link calls) falling back to static? Recommendation: ship
   static as the default, File API as an opt-in upgrade.
2. **Scope:** build all five phases (~4–6 wks), or stop after P1–P2 (any-codebase +
   multi-lang enforcement incl. C++) and treat retrieval/capture as a later push?
3. **JBS granularity:** for C++ the components fall out of CMake targets (122 of
   them) — keep them 1:1, or group targets into coarser subsystems? For the
   non-CMake parts, components per `apps/*` + `libs/*` child or per top-level dir?
