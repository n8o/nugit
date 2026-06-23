---
schema_version: 1
id: SPEC-003
type: spec
scope: global
status: active
created: 2026-06-23T00:00:00Z
relates_to:
  - elaborates:ADR-0012
  - constrains:deploy
provenance:
  commit: deployable-detector
confidence: high
---

# SPEC-003 — Deployable detector contract

The deterministic fact-extraction half of [[0012-ai-drafts-model-code-enforces]]:
how nugit derives a repo's **container inventory** (deployable units) and the
**evidence graph** the grounded-agent bootstrap may not contradict. Measured
against the JBS monorepo (~36 first-party containers); the *method* is general, the
accuracy numbers are repo-specific.

## Normative requirements

1. **Prune first.** All detection runs after excluding directories that hold no
   first-party deployables and inflate raw counts 7–20× (`.worktrees`,
   `.claude/worktrees`, `vendor`, `third_party`, `node_modules`, `build`, `target`,
   `dist`, `testdata`). On JBS this collapses 695 raw executables → 77 and 53 raw
   Dockerfiles → 37. **Skipping this step makes every estimate garbage.**

2. **Dockerfile is the spine.** A container candidate = a first-party Dockerfile
   that ships application code (`docker/Dockerfile.<name>` or a nested
   `*/Dockerfile`). The Dockerfile is the only signal that catches all languages and
   the multi-deployable nested cases. Each yields `{image, binary?, source_dir?,
   language, is_base_or_test}`.

3. **Exclude non-services deterministically.** Blocklist base/builder/sdk/test/init
   images and `*_bench`/`*_test` name patterns, and gate first-party by image
   registry prefix. A `*_bench` target with a dir + Dockerfile + `install(RUNTIME)`
   (every positive signal) is still **not** a service — the name blocklist wins.

4. **CMake `install(TARGETS … RUNTIME)` is the C++ confirmer**, restricted to
   `apps/**` (never `libs/`, never `third_party/`). Exactly one installed runtime
   target in a service dir + a matching Dockerfile binary = triple-signal agreement.

5. **Absence is legitimate.** Non-C++ services (Python/Rust/Go) have **no** CMake
   signal by design. The install detector returning empty must **not** veto a
   container that a first-party Dockerfile ships from `apps/<svc>/`.

6. **Components, not containers, for libraries.** `libs/*` (shared `add_library`,
   no installed runtime binary) are **components** linked by many containers via
   `target_link_libraries` — modelled as used-by-multiple, never as containers.

7. **Confidence tiers + evidence.** Every detected container carries a tier —
   `HIGH-3` (Dockerfile + install(RUNTIME) + apps-dir agree), `HIGH-2`
   (Dockerfile + apps-dir, CMake legitimately absent), or `NEEDS-AGENT` — and the
   **source line** for each signal. The grounded agent ([[0012-ai-drafts-model-code-enforces]])
   may group, name, and resolve `NEEDS-AGENT` cases, but may not invent a container
   or contradict a cited `COPY`/`install` line.

8. **Tie-breaks when signals disagree on one-vs-many.** Dockerfile count wins: a
   deployable is a Dockerfile that ships app code, not a top-level `apps/` dir (so a
   subsystem dir with N nested Dockerfiles → N containers). k8s/helm only *confirms*
   which containers are deployed and supplies the first-party image gate; it never
   *bounds* the set (most pods are operator/CRD-created at runtime).

## The deterministic/agent line

Deterministic detection produces a **correct but flat and partially-mislabelled**
inventory (~83% clean on JBS). It does **not** decide sub-system grouping, naming,
multi-deployable expansion judgement, or runtime-created pods — those are the
agent's, grounded by and forbidden to contradict this evidence.
