# nugit — re-shaped build plan

This replaces the original §11 epic ladder. The original plan put the only
demo-able win (the unified PR view) last, behind ~13 subsystems. A multi-agent
review found that sequencing maximizes the chance of stalling in infrastructure
before the thesis is ever validated. So we **invert it**: prove the keystone
first on the cheapest substrate, then add infrastructure only when a concrete
trigger fires.

See [ADR-0004](.nugit/decisions/0004-thin-keystone-first.md) for the rationale.

## Sequence

| Stage | Status | What | Gate |
|---|---|---|---|
| **S0 — Spike** | ✅ done | Prove the file→component mapping + significance heuristic on nugit's own Go code (`internal/goimports`, `internal/mapping`, `internal/significance`). | The C4↔code check resolves real imports to components and flags an undeclared edge. |
| **K1 — Thin keystone** | ✅ done | `nugit pr-render`: four deltas (C4 / code / knowledge / plan) + cross-artifact consistency + significance gating, rendered as markdown / check-run / JSON. No index, no content-addressing, no embeddings, no merge driver. | [SPEC-001](.nugit/specs/SPEC-001-thin-keystone.md) AC1–AC4 pass; `go test ./...` green; runs on nugit's own repo. |
| **K2 — Harden** | ✅ done | Package tests, GitHub Action wrapper (proven on PR #1), deterministic cost budget (ADR-0006). | CI posted a check-run on a real PR; render stays < 1s and LLM-free. |
| **A1 — Adopt anywhere** | ✅ done | `nugit init`: scaffold `.nugit/` + reverse-engineer a first-pass C4 model from the Go import graph; `config.yml` wired (warn-until-ratified `c4.mode`). | `init` on a brownfield repo yields a model that renders green; flipping to `enforce` gates. |
| **I1 — One fitness backend** | ⏳ conditional | Generate a `go-arch-lint` config from `workspace.dsl` and validate. | **Trigger:** a second language enters the repo, OR the import-graph check needs richer rules than `go/parser` gives. |
| **I2 — Index + retrieval** | ⏳ conditional | SQLite FTS over `.nugit/`, then `context(path)`. | **Trigger:** grep/scan over `.nugit/` becomes too slow for retrieval. |
| **I3 — Content-addressing + merge driver** | ⏳ conditional | Content hashes (as integrity, not keys — ADR-0001), the union merge driver. | **Trigger:** concurrent knowledge writes actually conflict in practice. |

**No silent caps:** infra stages are deferred, not cancelled. Each names the
observable signal that should pull it forward. Until that signal, building it is
speculative cost (ADR-0004).

## Format-freeze decisions (locked before any store format ships)

These were taken now because content-addressed IDs make later format changes a
breaking re-hash — they are cheap to decide today and unfixable later.

- [ADR-0001](.nugit/decisions/0001-schema-versioning.md) — stable cross-reference **keys** (not content hashes) + `schema_version` + migration policy.
- [ADR-0002](.nugit/decisions/0002-file-to-component-binding.md) — file→component binding via `properties { paths }` globs (the review's missing primitive).
- [ADR-0003](.nugit/decisions/0003-supersede-without-mutation.md) — supersede without mutation; effective status derived from the graph.
- [ADR-0005](.nugit/decisions/0005-squash-merge-capture.md) — capture survives squash-merge.
- [ADR-0006](.nugit/decisions/0006-per-pr-cost-budget.md) — per-PR compute budget; deterministic by default.
- [ADR-0007](.nugit/decisions/0007-hard-delete-and-gc.md) — hard-delete / erasure path despite immutable-in-git.

## Still open (surface, don't guess)

- Engine language: **Go** (decided).
- Greenfield vs extend compound-agent: building **greenfield** with nugit-native
  fallbacks; a compound-agent adapter is additive later. (Resolves the §13.2 ↔
  §10/§11 contradiction the review found.)
- An eval harness (labeled fixture corpus, precision/recall for retrieval and the
  significance classifier) — add when I2 lands, gate retrieval quality on it.
- ~~A brownfield bootstrap for adopting an existing repo~~ — **done** (A1:
  `nugit init` + warn-until-ratified `c4.mode`). Cross-language model bootstrap
  still waits on I1.
