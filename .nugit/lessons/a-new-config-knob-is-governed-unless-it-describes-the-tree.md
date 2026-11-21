---
schema_version: 1
id: LESSON-a-new-config-knob-is-governed-unless-it-describes-the-tree
type: lesson
scope: config
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:config
  - informs:ADR-0055
provenance:
  commit: seed
  citation: "nugit, 2026-11-21: engine.go read config.yml at the PR's head, so a PR could lower its own pr_render.fail_on (ADR-0055)"
confidence: high
diff_trigger:
  paths: ["internal/config/config.go"]
  added: 'yaml:"[a-z_]+"'
check: "Is this key governed (read from the target branch, the default), or does it only describe the reviewed tree? If the latter, add it to config.HeadKeys and Govern with the reason, and say why a PR may set it for itself."
---

# Lesson — a new config knob is governed unless it describes the tree

**Trigger:** a field is added to `config.Config`.

**Insight:** every key in config.yml is read from the branch the change
merges into, except the short list in `config.HeadKeys` (ADR-0055). That is
the safe default for a gate's mode or threshold: a PR must not set the rules
that judge it. It is the wrong default for a key that locates something in
the reviewed tree, as `c4.dsl` does. Such a key read from the target points
at a path the PR may have moved. Decide which kind the new key is when you
add it, because the default is silent either way.

**Rejected:** reading new keys at head until someone classifies them. That
fails open for exactly the knob nobody thought about.

**Keywords:** config, governed keys, enforcement, head keys, fail-on, gate
