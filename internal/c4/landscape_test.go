package c4

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/mapping"
)

// The canonical landscape from ADR-0034: one system that IS a repo, one shared
// system owned by a sibling, and a cross-system relationship.
const sampleLandscape = `workspace {
  model {
    gateway = softwareSystem "Consumer Gateway" {
      properties { "nugit_repo" "consumer-gateway" }
    }
    registry = softwareSystem "Shared artifact registry" "where builds land" {
      properties {
        "nugit_owner" "producer-service"
        "nugit_paths" "platform/registry/**,deploy/registry/*.yaml"
      }
    }
    gateway -> registry "pulls build artifacts"
  }
}`

func TestParseLandscapeValid(t *testing.T) {
	l := ParseLandscape(sampleLandscape)
	if len(l.Systems) != 2 {
		t.Fatalf("want 2 systems, got %d (%+v)", len(l.Systems), l.Systems)
	}
	gw, ok := l.System("gateway")
	if !ok || gw.Name != "Consumer Gateway" || gw.Repo != "consumer-gateway" {
		t.Errorf("gateway = %+v", gw)
	}
	if gw.Shared() {
		t.Error("a system with no nugit_owner is not shared")
	}
	reg, ok := l.System("registry")
	if !ok {
		t.Fatal("registry missing")
	}
	if reg.Owner != "producer-service" || !reg.Shared() {
		t.Errorf("registry ownership = %+v", reg)
	}
	if reg.Desc != "where builds land" {
		t.Errorf("positional description = %q", reg.Desc)
	}
	if len(reg.Paths) != 2 || reg.Paths[0] != "platform/registry/**" || reg.Paths[1] != "deploy/registry/*.yaml" {
		t.Errorf("nugit_paths = %+v", reg.Paths)
	}
	if !reg.OwnedBy("producer-service") || reg.OwnedBy("consumer-gateway") || reg.OwnedBy("") {
		t.Error("OwnedBy is string equality and an empty id never matches")
	}
	if len(l.Rels) != 1 || l.Rels[0].Src != "gateway" || l.Rels[0].Dst != "registry" ||
		l.Rels[0].Desc != "pulls build artifacts" {
		t.Errorf("rels = %+v", l.Rels)
	}
}

// THE LAYERING GUARANTEE (ADR-0034 point 2), pinned as a negative: a landscape
// contributes NO C4 element to the per-repo model. `softwareSystem` stays
// transparent exactly as ADR-0017 left it, so a landscape yields no components
// and no containers — which is what makes it structurally impossible for a
// landscape to reach mapping, Covered(), gen-rules, or the c4<->code gate. The
// two artifacts also live at two fixed, distinct paths and are parsed by two
// entry points, so neither file is ever handed to the other's parser.
func TestPerRepoParserSeesNoElementsInALandscape(t *testing.T) {
	m := Parse(sampleLandscape)
	if len(m.Components) != 0 || len(m.Containers) != 0 {
		t.Fatalf("c4.Parse must record no elements from a landscape.dsl, got %+v", m)
	}
	if !mapping.New(m).Empty() {
		t.Error("a landscape must produce no path bindings in the per-repo mapper")
	}
	if LandscapePath == ".nugit/architecture/workspace.dsl" {
		t.Fatal("the landscape must never share the per-repo model's path")
	}
	// And the converse: the landscape parser records nothing from a per-repo
	// model, because a workspace.dsl declares no softwareSystem properties.
	l := ParseLandscape(`workspace "x" { model { s = softwareSystem "S" {
	  app = container "App" { a = component "A" { properties { "paths" "a/**" } } a -> b } } } }`)
	if len(l.Systems) != 1 {
		t.Fatalf("want the one system, got %+v", l.Systems)
	}
	if s := l.Systems[0]; s.Repo != "" || s.Owner != "" || len(s.Paths) != 0 {
		t.Errorf("a plain workspace.dsl system must carry no landscape facts: %+v", s)
	}
	if len(l.Rels) != 0 {
		t.Errorf("component-level edges inside a container must not become landscape rels: %+v", l.Rels)
	}
}

// Malformed input degrades to whatever was understood — never a panic, never an
// error. A landscape may be authored in a repo this one does not own, so it
// must not be able to break this repo's tooling.
func TestParseLandscapeMalformedDegrades(t *testing.T) {
	cases := map[string]string{
		"unterminated block":  `workspace { model { a = softwareSystem "A" { properties { "nugit_owner" "b"`,
		"unterminated string": `workspace { model { a = softwareSystem "A`,
		"stray braces":        `} } } workspace { model { a = softwareSystem "A" {} } }`,
		"empty":               ``,
		"junk":                "\x00\x01 not a dsl at all ->-> {{{",
		"missing keyword":     `workspace { model { a = "A" { properties { "nugit_owner" "b" } } } }`,
		"arrow to nowhere":    `workspace { model { a = softwareSystem "A" {} a -> } }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			l := ParseLandscape(src) // must not panic
			for _, s := range l.Systems {
				if s.ID == "" {
					t.Errorf("recorded a system with no id from %q: %+v", src, l.Systems)
				}
			}
		})
	}
	// The first case still understood the system it got to.
	if l := ParseLandscape(`workspace { model { a = softwareSystem "A" { properties { "nugit_owner" "b"`); len(l.Systems) != 1 || l.Systems[0].Owner != "b" {
		t.Errorf("truncated input should keep what was understood: %+v", l.Systems)
	}
}

// A views block carries its own arrows and element refs; they must never leak
// into the landscape (the same hazard Parse guards against).
func TestParseLandscapeViewsDoNotLeak(t *testing.T) {
	l := ParseLandscape(`workspace { model {
	    a = softwareSystem "A" {}
	    b = softwareSystem "B" {}
	    a -> b "real"
	  }
	  views {
	    systemLandscape "L" { include * }
	    dynamic * "flow" { b -> a "phantom" }
	    styles { element "Element" { background "#fff" } }
	  }
	}`)
	if len(l.Systems) != 2 {
		t.Fatalf("views leaked systems: %+v", l.Systems)
	}
	if len(l.Rels) != 1 || l.Rels[0].Desc != "real" {
		t.Errorf("views leaked relationships: %+v", l.Rels)
	}
}

// An invalid glob matches nothing, so it must be REPORTED, never silently
// dropped — the mapping.InvalidPatterns() discipline one layer up.
func TestLandscapeInvalidGlobsReported(t *testing.T) {
	l := ParseLandscape(`workspace { model {
	  s = softwareSystem "S" { properties { "nugit_owner" "o" "nugit_paths" "good/**,[bad" } }
	}}`)
	bad := LandscapeInvalidGlobs(l)
	if len(bad) != 1 || bad[0].System != "s" || bad[0].Pattern != "[bad" {
		t.Fatalf("invalid globs = %+v", bad)
	}
	sys := l.Systems[0]
	if !ConfiguresPath(sys, "good/x.yaml") {
		t.Error("the valid glob must still bind")
	}
	if ConfiguresPath(sys, "[bad") {
		t.Error("an invalid glob must match nothing, not even itself")
	}
}

func TestConfiguringMatchesAndSorts(t *testing.T) {
	l := ParseLandscape(sampleLandscape)
	if got := Configuring(l, "platform/registry/retention.yaml"); len(got) != 1 || got[0].ID != "registry" {
		t.Errorf("Configuring = %+v", got)
	}
	if got := Configuring(l, "./deploy/registry/prod.yaml"); len(got) != 1 {
		t.Errorf("a ./-prefixed path must normalize: %+v", got)
	}
	if got := Configuring(l, "deploy/registry/nested/prod.yaml"); len(got) != 0 {
		t.Errorf("a single-star glob must not cross a slash: %+v", got)
	}
	if got := Configuring(l, "cmd/main.go"); len(got) != 0 {
		t.Errorf("unrelated path matched: %+v", got)
	}
	m := ConfiguringAny(l, []string{"cmd/main.go", "platform/registry/b.yaml", "platform/registry/a.yaml"})
	got := m["registry"]
	if len(got) != 2 || got[0] != "platform/registry/a.yaml" || got[1] != "platform/registry/b.yaml" {
		t.Errorf("ConfiguringAny must return sorted matches: %+v", m)
	}
}

// ---- resolution (ADR-0011 single-writer) ----

func TestResolveLandscapeLocalWins(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Path: "local.dsl", Src: sampleLandscape},
		{Name: "peerA", Path: "a.dsl", Src: `workspace { model { z = softwareSystem "Z" {} } }`},
	})
	if !res.Found || res.From != "" || res.Landscape.Origin != "" {
		t.Fatalf("local must win outright: %+v", res)
	}
	if _, ok := res.Landscape.System("z"); ok {
		t.Error("a peer landscape must not be merged into the local one")
	}
	if res.Landscape.OriginLabel() != "local" {
		t.Errorf("OriginLabel = %q", res.Landscape.OriginLabel())
	}
}

func TestResolveLandscapeFallsBackToTheSinglePeer(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Name: "producer", Path: ".nugit/architecture/landscape.dsl", Src: sampleLandscape},
	})
	if !res.Found || res.From != "producer" || res.Landscape.Origin != "producer" {
		t.Fatalf("single peer should win: %+v", res)
	}
	if res.Landscape.OriginLabel() != "peer producer" {
		t.Errorf("OriginLabel = %q — a reader must never mistake it for local", res.Landscape.OriginLabel())
	}
}

// The ambiguous case is the one resolution deliberately REFUSES to decide:
// picking by configured order would make the org's shared model depend on the
// reader's private, reorderable peer list.
func TestResolveLandscapeAmbiguousUsesNothing(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Name: "zeta", Src: sampleLandscape},
		{Name: "alpha", Src: sampleLandscape},
	})
	if res.Found {
		t.Fatal("two peers declaring a landscape must resolve to NOTHING, not to the first")
	}
	if len(res.Ambiguous) != 2 || res.Ambiguous[0] != "alpha" || res.Ambiguous[1] != "zeta" {
		t.Errorf("Ambiguous must name every claimant, sorted: %+v", res.Ambiguous)
	}
}

// An empty or unparseable file is not a claim, so it can neither win nor create
// an ambiguity.
func TestResolveLandscapeEmptySourcesAreNotClaims(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Path: "local.dsl", Src: "   \n"},
		{Name: "a", Src: "not a dsl"},
		{Name: "b", Src: sampleLandscape},
	})
	if !res.Found || res.From != "b" {
		t.Fatalf("only b made a claim: %+v", res)
	}
	if got := ResolveLandscape(nil); got.Found || len(got.Ambiguous) != 0 {
		t.Errorf("no sources = no landscape: %+v", got)
	}
}

func TestReadLandscapeFromDirs(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := ReadLandscape(dir); ok {
		t.Error("an absent landscape must read as not-ok, never an error")
	}
	p := filepath.Join(dir, filepath.FromSlash(LandscapePath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(sampleLandscape), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs := LandscapeSourcesFromDirs([]LandscapeDir{{Dir: dir}, {Name: "gone", Dir: filepath.Join(dir, "nope")}})
	if len(srcs) != 1 || srcs[0].Name != "" || srcs[0].Path != LandscapePath {
		t.Fatalf("sources = %+v", srcs)
	}
	if res := ResolveLandscape(srcs); !res.Found || len(res.Landscape.Systems) != 2 {
		t.Errorf("resolution from disk = %+v", res)
	}
}

// ---- render ----

func TestLandscapeMermaid(t *testing.T) {
	got := LandscapeMermaid(ParseLandscape(sampleLandscape))
	want := "graph LR\n" +
		"  gateway[\"Consumer Gateway (repo consumer-gateway)\"]\n" +
		"  registry[[\"Shared artifact registry (shared · owned by producer-service)\"]]\n" +
		"  gateway -->|pulls build artifacts| registry\n"
	if got != want {
		t.Errorf("landscape mermaid drifted\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Labels must be escaped the way render.go escapes them, plus the pipe that
// only edge labels can be broken by.
func TestLandscapeMermaidEscapesAndSorts(t *testing.T) {
	got := LandscapeMermaid(ParseLandscape(`workspace { model {
	  zulu = softwareSystem "Z [x] \"q\"" {}
	  alpha = softwareSystem "A" {}
	  alpha -> zulu "a|b
c"
	}}`))
	if !strings.HasPrefix(got, "graph LR\n  alpha[\"A\"]\n  zulu[") {
		t.Errorf("systems must sort by id:\n%s", got)
	}
	if strings.Contains(got, `Z [x]`) || !strings.Contains(got, "&#91;x&#93;") || !strings.Contains(got, "&quot;q&quot;") {
		t.Errorf("node label not escaped:\n%s", got)
	}
	if !strings.Contains(got, "-->|a&#124;b c| zulu") {
		t.Errorf("edge label must escape the pipe and flatten newlines:\n%s", got)
	}
}
