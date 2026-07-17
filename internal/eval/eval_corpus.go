package eval

import "github.com/n8o/nugit/internal/model"

// Shared base: three components (a,b,c); a depends on b (declared). Clean.
const baseDSL = `workspace "m" {
  model {
    s = softwareSystem "m" {
      a = component "A" { properties { paths "a/**" } }
      b = component "B" { properties { paths "b/**" } }
      c = component "C" { properties { paths "c/**" } }
      a -> b
    }
  }
}`

// baseDSL + a declared a->c edge (for the architectural-but-clean case).
const dslWithAC = `workspace "m" {
  model {
    s = softwareSystem "m" {
      a = component "A" { properties { paths "a/**" } }
      b = component "B" { properties { paths "b/**" } }
      c = component "C" { properties { paths "c/**" } }
      a -> b
      a -> c
    }
  }
}`

var baseFiles = map[string]string{
	"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\nfunc A() {}\n",
	"b/b.go": "package b\n\nfunc B() {}\n",
	"c/c.go": "package c\n\nfunc C() {}\n",
}

// leveledDSL builds a two-level fixture: containers X{x1,x2}, Y{y1} (Y also
// binds its own ycore/** paths), Z{z1}, plus the given relationship lines.
// Fully anonymized (redaction constraint: generic ids only, never pilot names).
func leveledDSL(edges ...string) string {
	s := `workspace "m" {
  model {
    sys = softwareSystem "m" {
      X = container "X" {
        x1 = component "X1" { properties { paths "x1/**" } }
        x2 = component "X2" { properties { paths "x2/**" } }
      }
      Y = container "Y" {
        properties { "paths" "ycore/**" }
        y1 = component "Y1" { properties { paths "y1/**" } }
      }
      Z = container "Z" {
        z1 = component "Z1" { properties { paths "z1/**" } }
      }
`
	for _, e := range edges {
		s += "      " + e + "\n"
	}
	return s + `    }
  }
}`
}

var leveledFiles = map[string]string{
	"x1/x1.go":      "package x1\n\nfunc X1() {}\n",
	"x2/x2.go":      "package x2\n\nfunc X2() {}\n",
	"y1/y1.go":      "package y1\n\nfunc Y1() {}\n",
	"ycore/core.go": "package ycore\n\nfunc Core() {}\n",
	"z1/z1.go":      "package z1\n\nfunc Z1() {}\n",
}

const x1ImportsY1 = "package x1\n\nimport _ \"example.com/m/y1\"\n\nfunc X1() {}\n"
const ycoreImportsZ1 = "package ycore\n\nimport _ \"example.com/m/z1\"\n\nfunc Core() {}\n"

func adr(id, scope, supersedes string) string {
	return adrWith(id, scope, "accepted", supersedes)
}

func adrWith(id, scope, status, supersedes string) string {
	s := "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: " + scope +
		"\nstatus: " + status + "\ncreated: 2026-01-01T00:00:00Z\n"
	if supersedes != "" {
		s += "supersedes: " + supersedes + "\n"
	}
	return s + "provenance:\n  commit: x\n---\n\n# " + id + "\n"
}

func lesson(id, scope string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: lesson\nscope: " + scope +
		"\nstatus: active\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + "\n"
}

const warnConfig = "schema_version: 1\nc4:\n  mode: warn\n"

// bigFunc is >20 lines of churn in a single component.
const bigFunc = "package a\n\nimport _ \"example.com/m/b\"\n\nfunc A() {\n" +
	"\t_ = 1\n\t_ = 2\n\t_ = 3\n\t_ = 4\n\t_ = 5\n\t_ = 6\n\t_ = 7\n\t_ = 8\n" +
	"\t_ = 9\n\t_ = 10\n\t_ = 11\n\t_ = 12\n\t_ = 13\n\t_ = 14\n\t_ = 15\n" +
	"\t_ = 16\n\t_ = 17\n\t_ = 18\n\t_ = 19\n\t_ = 20\n\t_ = 21\n\t_ = 22\n}\n"

var corpus = []Case{
	{
		Name:      "trivial-comment",
		Head:      map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// tweak.\nfunc A() {}\n"},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		Name: "feature-two-components",
		Head: map[string]string{
			"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// a edit.\nfunc A() {}\n",
			"b/b.go": "package b\n\n// b edit.\nfunc B() {}\n",
		},
		WantTier:  model.TierFeature,
		WantClean: true,
	},
	{
		// A model change with no accompanying ADR correctly draws a
		// decision-coverage warning (architectural change, no recorded why).
		Name:       "architectural-model-change-no-adr",
		DSLHead:    dslWithAC,
		Head:       map[string]string{"a/a.go": "package a\n\nimport (\n\t_ \"example.com/m/b\"\n\t_ \"example.com/m/c\"\n)\n\nfunc A() {}\n"},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"decision-coverage"},
		WantClean:  true,
	},
	{
		Name:       "undeclared-edge-enforce",
		Head:       map[string]string{"b/b.go": "package b\n\nimport _ \"example.com/m/a\"\n\nfunc B() {}\n"},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		Name:       "undeclared-edge-warn-mode",
		Config:     warnConfig,
		Head:       map[string]string{"b/b.go": "package b\n\nimport _ \"example.com/m/a\"\n\nfunc B() {}\n"},
		WantTier:   model.TierTrivial, // warn mode: an undeclared edge is not (yet) architectural
		WantChecks: []string{"c4<->code"},
		WantClean:  true,
	},
	{
		Name: "stale-knowledge",
		Base: mergeFiles(baseFiles, map[string]string{
			".nugit/decisions/old.md": adr("ADR-OLD", "a", ""),
			".nugit/decisions/new.md": adr("ADR-NEW", "a", "ADR-OLD"),
		}),
		Head:       map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// touch governed code.\nfunc A() {}\n"},
		WantTier:   model.TierTrivial,
		WantChecks: []string{"stale-knowledge"},
		WantClean:  true,
	},
	{
		Name: "decision-coverage-satisfied",
		Head: map[string]string{
			"b/b.go":                  "package b\n\nimport _ \"example.com/m/a\"\n\nfunc B() {}\n",
			".nugit/decisions/why.md": adr("ADR-WHY", "b", ""),
		},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code"}, // decision-coverage satisfied by the new ADR
		WantClean:  false,
	},
	{
		// ADR-0016 candidate lane: a `status: proposed` ADR is a draft, not
		// ratified knowledge — decision-coverage must still warn.
		Name: "proposed-adr-does-not-cover",
		Head: map[string]string{
			"b/b.go":                  "package b\n\nimport _ \"example.com/m/a\"\n\nfunc B() {}\n",
			".nugit/decisions/why.md": adrWith("ADR-WHY", "b", "proposed", ""),
		},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		Name:       "spec-linkage-unknown",
		Head:       map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// edit.\nfunc A() {}\n"},
		HeadMsg:    "fix: thing\n\nlearned: keep specs in tree\nkeywords: spec, test\nspec: SPEC-404\n",
		WantTier:   model.TierTrivial,
		WantChecks: []string{"spec-linkage"},
		WantClean:  true,
	},
	{
		Name:       "capture-hygiene-missing-fields",
		Head:       map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\n// edit.\nfunc A() {}\n"},
		HeadMsg:    "feat: thing\n\ndecision: do it\naffects: a\n", // missing learned: + keywords:
		WantTier:   model.TierTrivial,
		WantChecks: []string{"capture-hygiene"},
		WantClean:  true,
	},
	{
		Name:      "test-import-not-flagged",
		Head:      map[string]string{"a/a_test.go": "package a\n\nimport _ \"example.com/m/c\"\n"}, // a->c undeclared, but _test
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		Name:      "knowledge-only-lesson",
		Head:      map[string]string{".nugit/lessons/l.md": lesson("LESSON-x", "a")},
		WantTier:  model.TierFeature, // knowledge changed
		WantClean: true,
	},
	{
		Name:      "large-churn-single-component",
		Head:      map[string]string{"a/a.go": bigFunc},
		WantTier:  model.TierFeature, // > trivial churn threshold
		WantClean: true,
	},

	// ---- two-level enforcement (ADR-0017): containers + roll-up ----
	{
		// Cross-container dependency declared at COMPONENT level: clean.
		Name:      "leveled-component-edge-clean",
		DSL:       leveledDSL("x1 -> y1"),
		Base:      leveledFiles,
		Head:      map[string]string{"x1/x1.go": x1ImportsY1},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		// Same dependency declared ONLY as the container edge X -> Y: the
		// roll-up covers it — clean.
		Name:      "leveled-container-rollup-clean",
		DSL:       leveledDSL("X -> Y"),
		Base:      leveledFiles,
		Head:      map[string]string{"x1/x1.go": x1ImportsY1},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		// Undeclared at either level: fires and is architectural.
		Name:       "leveled-cross-undeclared",
		DSL:        leveledDSL(),
		Base:       leveledFiles,
		Head:       map[string]string{"x1/x1.go": x1ImportsY1},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		// A file owned by the CONTAINER's own paths (ycore/**) importing a
		// foreign child: fires with the container id as the source.
		Name:       "leveled-container-src-undeclared",
		DSL:        leveledDSL(),
		Base:       leveledFiles,
		Head:       map[string]string{"ycore/core.go": ycoreImportsZ1},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		// Same import, container edge Y -> Z declared: clean.
		Name:      "leveled-container-src-covered",
		DSL:       leveledDSL("Y -> Z"),
		Base:      leveledFiles,
		Head:      map[string]string{"ycore/core.go": ycoreImportsZ1},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
}

func mergeFiles(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
