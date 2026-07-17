package eval

import "github.com/n8o/nugit/internal/model"

// Adversarial cases: spoofs and near-misses designed to make each consistency
// check fire when it must not (or stay silent when it must not). The main
// corpus measures accuracy on representative changes; this set attacks the
// checks' edges. Gate: every case must pass exactly (TestAdversarial) — no
// precision/recall slack for hand-built spoofs.

// dslDeepGlob adds a component whose glob nests INSIDE another component's
// glob ("b/deep/**" inside "b/**"); most-specific-prefix resolution must route
// b/deep files to d, not b.
const dslDeepGlob = `workspace "m" {
  model {
    s = softwareSystem "m" {
      a = component "A" { properties { paths "a/**" } }
      b = component "B" { properties { paths "b/**" } }
      c = component "C" { properties { paths "c/**" } }
      d = component "D" { properties { paths "b/deep/**" } }
      a -> b
      a -> d
    }
  }
}`

// dslDeepGlobNoEdge is the twin without the a -> d declaration.
const dslDeepGlobNoEdge = `workspace "m" {
  model {
    s = softwareSystem "m" {
      a = component "A" { properties { paths "a/**" } }
      b = component "B" { properties { paths "b/**" } }
      c = component "C" { properties { paths "c/**" } }
      d = component "D" { properties { paths "b/deep/**" } }
      a -> b
    }
  }
}`

const deepImport = "package a\n\nimport (\n\t_ \"example.com/m/b\"\n\t_ \"example.com/m/b/deep\"\n)\n\nfunc A() {}\n"

// fullTrailer is a complete block: learned:+keywords: present so
// capture-hygiene stays silent, decision: present for coverage cases.
const fullTrailer = "\n\ndecision: switched to X\nrejected: Y was slower\nlearned: X beats Y under load\naffects: b\nkeywords: x, load"

var adversarial = []Case{
	{
		// The import LOOKS like an undeclared a->b/deep edge if globs resolve
		// naively; the deeper "b/deep/**" glob routes it to d, and a -> d IS
		// declared. Must stay silent.
		Name:      "deep-glob-edge-declared",
		DSL:       dslDeepGlob,
		Base:      mergeFiles(baseFiles, map[string]string{"b/deep/deep.go": "package deep\n\nfunc D() {}\n"}),
		Head:      map[string]string{"a/a.go": deepImport},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		// Positive twin: same import, no a -> d declaration — must fire.
		Name:       "deep-glob-edge-undeclared",
		DSL:        dslDeepGlobNoEdge,
		Base:       mergeFiles(baseFiles, map[string]string{"b/deep/deep.go": "package deep\n\nfunc D() {}\n"}),
		Head:       map[string]string{"a/a.go": deepImport},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		// A superseded object governs component c; the PR touches only a.
		// stale-knowledge must not nag about knowledge the change can't rot.
		Name: "stale-knowledge-scoped-elsewhere",
		Base: mergeFiles(baseFiles, map[string]string{
			".nugit/decisions/old.md": adr("ADR-OLD", "c", ""),
			".nugit/decisions/new.md": adr("ADR-NEW", "c", "ADR-OLD"),
		}),
		Head:      map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// touch a only.\nfunc A() {}\n"},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		// The PR touches governed code AND updates the stale object itself —
		// the exact remediation the warning asks for must not re-trigger it.
		Name: "stale-knowledge-object-updated",
		Base: mergeFiles(baseFiles, map[string]string{
			".nugit/decisions/old.md": adr("ADR-OLD", "a", ""),
			".nugit/decisions/new.md": adr("ADR-NEW", "a", "ADR-OLD"),
		}),
		Head: map[string]string{
			"a/a.go":                  "package a\n\nimport _ \"example.com/m/b\"\n\n// governed edit.\nfunc A() {}\n",
			".nugit/decisions/old.md": adr("ADR-OLD", "a", "") + "\nSuperseded by ADR-NEW; kept for the audit trail.\n",
		},
		WantTier:  model.TierFeature, // knowledge changed
		WantClean: true,
	},
	{
		// Architectural change whose why is recorded ONLY as a trailer — the
		// ADR-0005 capture primitive satisfies decision-coverage on its own,
		// and a complete block keeps capture-hygiene silent too.
		Name:       "decision-coverage-trailer-only",
		Head:       map[string]string{"b/b.go": "package b\n\nimport _ \"example.com/m/a\"\n\nfunc B() {}\n"},
		HeadMsg:    "feat: swap direction" + fullTrailer,
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code"},
		WantClean:  false,
	},
	{
		// A complete trailer block on a trivial change: nothing may fire —
		// capture-hygiene only polices INCOMPLETE blocks.
		Name:      "capture-hygiene-complete-block",
		Head:      map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// edit.\nfunc A() {}\n"},
		HeadMsg:   "fix: tweak" + fullTrailer,
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		// A non-test file whose name merely CONTAINS "test": the _test.go
		// exclusion must not be a substring match — this undeclared a->c
		// import must fire.
		Name:       "test-exclusion-not-overbroad",
		Head:       map[string]string{"a/testutil.go": "package a\n\nimport _ \"example.com/m/c\"\n"},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		// The spec id is derivable from the FILENAME (SPEC-014-cache.md), even
		// though the front-matter id differs — spec-linkage must stay silent.
		Name: "spec-linkage-filename-id",
		Base: mergeFiles(baseFiles, map[string]string{
			".nugit/specs/SPEC-014-cache.md": specObj("SPEC-cache"),
		}),
		Head:      map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// edit.\nfunc A() {}\n"},
		HeadMsg:   "fix: cache path\n\nlearned: specs resolve by filename too\nkeywords: spec, cache\nspec: SPEC-014",
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
}

func specObj(id string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: spec\nscope: a\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + "\n"
}
