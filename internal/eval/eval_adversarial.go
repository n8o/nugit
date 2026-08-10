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
		// A stale object path-bound to third_party/** while the PR touches a
		// DIFFERENT unmapped infra file — the binding must not become a
		// wildcard nag (ADR-0020).
		Name: "stale-path-bound-elsewhere",
		Base: mergeFiles(baseFiles, map[string]string{
			"third_party/versions.env": "PIN=1\n",
			"helm/values.yaml":         "replicas: 1\n",
			".nugit/decisions/old.md":  adrPaths("ADR-OLD", "global", "accepted", "", "third_party/**"),
			".nugit/decisions/new.md":  adr("ADR-NEW", "global", "ADR-OLD"),
		}),
		Head:      map[string]string{"helm/values.yaml": "replicas: 2\n"},
		WantTier:  model.TierTrivial,
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
		// The same prose declaration WITH the front-matter edge (ADR-0022's
		// negative twin): prose and graph agree — must stay silent.
		Name: "prose-supersession-edge-present",
		Base: mergeFiles(baseFiles, map[string]string{
			".nugit/decisions/old.md": adr("ADR-OLD", "a", ""),
		}),
		Head: map[string]string{
			".nugit/decisions/new.md": adrProse("ADR-NEW", "a", "ADR-OLD",
				"Supersedes ADR-OLD — the old rule is stale."),
		},
		WantTier:  model.TierFeature, // knowledge changed
		WantClean: true,
	},
	{
		// ADR-0039's negative twin: a NEAR-MISS id. The grouping is exact
		// equality over (origin, id) — a shared prefix is two distinct records
		// and must stay silent, or every numbered family would collide.
		Name: "duplicate-id-near-miss",
		Base: mergeFiles(baseFiles, map[string]string{
			".nugit/decisions/0027.md": adr("ADR-P-0027", "a", ""),
		}),
		Head: map[string]string{
			".nugit/decisions/0027-b.md": adr("ADR-P-0027-B", "a", ""),
		},
		WantTier:  model.TierFeature, // knowledge changed
		WantClean: true,
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

	// ---- two-level roll-up: attacks on the Covered() boundary (ADR-0017) ----
	{
		// The roll-up hole, attacked from inside: X -> Y is declared, but the
		// new dependency is INTRA-X (x1 -> x2). A container edge must NEVER
		// cover an intra-container pair — this must fire.
		Name:       "rollup-never-covers-intra-container",
		DSL:        leveledDSL("X -> Y"),
		Base:       leveledFiles,
		Head:       map[string]string{"x1/x1.go": "package x1\n\nimport _ \"example.com/m/x2\"\n\nfunc X1() {}\n"},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		// X -> Y is declared but the import goes to a THIRD container's child
		// (x1 -> z1): the declared edge is aimed elsewhere — must fire.
		Name:       "rollup-wrong-target-container",
		DSL:        leveledDSL("X -> Y"),
		Base:       leveledFiles,
		Head:       map[string]string{"x1/x1.go": "package x1\n\nimport _ \"example.com/m/z1\"\n\nfunc X1() {}\n"},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
	{
		// A container-owned file (ycore/**) importing the container's OWN child
		// component is containment, not a dependency — must stay silent.
		Name:      "container-own-file-to-own-child-silent",
		DSL:       leveledDSL(),
		Base:      leveledFiles,
		Head:      map[string]string{"ycore/core.go": "package ycore\n\nimport _ \"example.com/m/y1\"\n\nfunc Core() {}\n"},
		WantTier:  model.TierTrivial,
		WantClean: true,
	},
	{
		// Pilot-SHAPED (fully anonymized) model: group-wrapped one-component
		// containers with _c ids, a shared-libs container, a blockless
		// container. An undeclared cross-container _c -> _c import must fire
		// exactly like a flat undeclared edge.
		Name:       "pilot-shape-cross-container-undeclared",
		DSL:        pilotShapeDSL,
		Base:       pilotShapeFiles,
		Head:       map[string]string{"svc/ingest/ingest.go": "package ingest\n\nimport (\n\t_ \"example.com/m/lib/x\"\n\t_ \"example.com/m/svc/encoder\"\n)\n\nfunc Ingest() {}\n"},
		WantTier:   model.TierArchitectural,
		WantChecks: []string{"c4<->code", "decision-coverage"},
		WantClean:  false,
	},
}

// pilotShapeDSL mirrors only the SHAPE of a real leveled model — group blocks,
// one-component-per-container with _c suffixes, a shared-libs wrapper, a
// blockless container. All names are generic by design (redaction constraint).
const pilotShapeDSL = `workspace "m" {
  model {
    sys = softwareSystem "m" {
      group "Services" {
        ingest = container "ingest" {
          ingest_c = component "ingest" { properties { paths "svc/ingest/**" } }
        }
        encoder = container "encoder" {
          encoder_c = component "encoder" { properties { paths "svc/encoder/**" } }
        }
        publisher = container "publisher" {
          publisher_c = component "publisher" { properties { paths "svc/publisher/**" } }
        }
      }
      shared = container "shared-libs" {
        libx = component "libx" { properties { paths "lib/x/**" } }
        liby = component "liby" { properties { paths "lib/y/**" } }
      }
      edge = container "edge"
      ingest_c -> libx
      encoder_c -> libx
      publisher_c -> liby
    }
  }
}`

var pilotShapeFiles = map[string]string{
	"svc/ingest/ingest.go":       "package ingest\n\nimport _ \"example.com/m/lib/x\"\n\nfunc Ingest() {}\n",
	"svc/encoder/encoder.go":     "package encoder\n\nimport _ \"example.com/m/lib/x\"\n\nfunc Encode() {}\n",
	"svc/publisher/publisher.go": "package publisher\n\nimport _ \"example.com/m/lib/y\"\n\nfunc Publish() {}\n",
	"lib/x/x.go":                 "package x\n\nfunc X() {}\n",
	"lib/y/y.go":                 "package y\n\nfunc Y() {}\n",
}

func specObj(id string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: spec\nscope: a\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + "\n"
}
