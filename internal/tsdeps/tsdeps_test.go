package tsdeps

import "testing"

func TestParseDepcruise(t *testing.T) {
	j := `{"modules":[
		{"source":"src/app/main.ts","dependencies":[
			{"resolved":"src/lib/core.ts","coreModule":false,"couldNotResolve":false},
			{"resolved":"src/lib/util.ts","coreModule":false},
			{"resolved":"node_modules/lodash/index.js","coreModule":false},
			{"resolved":"fs","coreModule":true},
			{"resolved":"","couldNotResolve":true}
		]},
		{"source":"src/lib/core.ts","dependencies":[]},
		{"source":"node_modules/lodash/index.js","dependencies":[]}
	]}`
	g := ParseDepcruise([]byte(j))

	dirs := map[string]bool{}
	for _, d := range g.Dirs {
		dirs[d] = true
	}
	if !dirs["src/app"] || !dirs["src/lib"] {
		t.Errorf("want src/app, src/lib dirs; got %v", g.Dirs)
	}
	if dirs["node_modules/lodash"] {
		t.Error("node_modules must be excluded")
	}
	edges := map[[2]string]bool{}
	for _, e := range g.Edges {
		edges[e] = true
	}
	if !edges[[2]string{"src/app", "src/lib"}] {
		t.Errorf("want src/app -> src/lib; got %v", g.Edges)
	}
	// external/core/unresolved deps dropped; src/lib has no first-party deps
	if len(g.Edges) != 1 {
		t.Errorf("want exactly 1 edge, got %v", g.Edges)
	}
}

func TestDiscoverDegradesWhenAbsent(t *testing.T) {
	// dependency-cruiser isn't installed in CI; Discover must report available=false
	// (not crash or silently pass).
	if Available() {
		t.Skip("depcruise present; degradation path not exercised")
	}
	g, avail, err := Discover(t.TempDir())
	if err != nil || avail || len(g.Dirs) != 0 {
		t.Errorf("absent depcruise must yield (empty, false, nil); got %v %v %v", g, avail, err)
	}
}
