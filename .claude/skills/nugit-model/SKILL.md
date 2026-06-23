---
name: nugit-model
description: Use to bootstrap or refresh a repo's C4 model (`.nugit/architecture/workspace.dsl`) from source — the ADR-0012 grounded-agent step. Trigger when adopting nugit on a multi-service/monorepo, when `nugit init`'s flat model needs leveling into services-as-containers, or when the user asks to (re)derive the architecture model.
---

# nugit-model — draft the C4 model, grounded by deterministic facts

This is the **AI-proposes** half of [ADR-0012]: you (the agent) turn nugit's
deterministic ground truth into a *grouped, named* `workspace.dsl`. nugit owns the
facts and the schema; you own the abstraction; the human ratifies via a warn-mode PR.

## 1. Get the ground truth — never invent structure

```sh
nugit model facts -C <repo> -format json
```

This returns: the **container inventory** (each with a confidence tier `HIGH-3` /
`HIGH-2` / `NEEDS-AGENT`, source dir, language, evidence, deploy-confirmation), the
**dependency edges** (`[srcDir, dstDir]` from the build graph), the **shared libs**, and
`deployed_not_detected` gaps. This is **ground truth you may not contradict**:

- Every C4 **container** must trace to a detected container or a `deployed_not_detected`
  gap. Do not invent containers.
- **`libs/*` are components**, never containers — model them as shared components used
  by the containers that link them (via the dependency edges).
- Keep each container's `source_dir`, `language`, and `evidence` as given.

## 2. Do the abstraction — what code can't

- **Group** containers into named sub-systems / bounded contexts (the deterministic layer
  can't — nothing in the files says which context a service belongs to). Infer from
  naming, directory clustering, and the dependency graph.
- **Resolve the `NEEDS-AGENT` tail**: multi-deployable subsystems (one dir → N nested
  Dockerfiles) become N containers; reconcile name skews against the gaps (e.g. a
  `frontend` candidate deployed as `orchestrator-frontend`); decide whether a
  `*-sidecar` is a peer container or a sidecar.
- **Name** containers and components legibly.

## 3. Emit valid Structurizr DSL (this is enforced)

`system → container → component`, **multi-line quoted properties** (the local
Structurizr parser rejects inline `properties { paths "x" }` — see ADR/SPEC):

```
sys = softwareSystem "Repo" {
  encoder = container "AAC Encoder" "media service" "C++" {
    properties {
      "paths" "apps/aac_encoder_service/**"
    }
  }
}
```

Bind each container to its `source_dir` via `properties { "paths" "<dir>/**" }`. Add
container→container and container→component relationships from the dependency edges.

## 4. Verify, then land it as a reviewed PR

```sh
nugit pr-render -C <repo> -base <main> -head HEAD   # must parse + self-check clean
nugit c4 preview -C <repo>                          # optional: eyeball the diagram
```

Write `.nugit/architecture/workspace.dsl` on a branch in **warn mode**, open a PR, and
stop — the human ratifies and flips to enforce. You propose; you do not merge.
