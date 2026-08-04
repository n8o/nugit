package modelfacts

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/cmake"
	"github.com/n8o/nugit/internal/deploy"
)

// Unit is one detected buildable or deployable code unit — the currency of the
// model-drift check (ADR-0021). Every Unit should correspond to a workspace.dsl
// element; one that doesn't is model drift.
type Unit struct {
	Dir      string `json:"dir"`      // nugit-root-relative source directory
	Name     string `json:"name"`     // detector name (container image, else dir base)
	Kind     string `json:"kind"`     // "deployable" | "cmake" | "go"
	Evidence string `json:"evidence"` // why the detector believes this unit exists
}

// Units inventories repoDir's detected units from the SAME detectors that
// ground the ADR-0012 bootstrap: deployable containers (SPEC-003), CMake
// target dirs, and Go package dirs (Go is an enforced backend, so its package
// layout is unit-shaped by convention). Best-effort and deterministic; units
// are deduped by directory with deployable evidence preferred over buildable.
func Units(repoDir string) []Unit {
	byDir := map[string]Unit{}
	add := func(u Unit) {
		if u.Dir == "" || u.Dir == "." {
			return // the repo root is the system, not a sub-unit
		}
		if _, ok := byDir[u.Dir]; !ok {
			byDir[u.Dir] = u
		}
	}
	if containers, _, err := deploy.Detect(repoDir); err == nil {
		for _, c := range containers {
			add(Unit{Dir: c.SourceDir, Name: c.Name, Kind: "deployable",
				Evidence: "dockerfile " + c.Dockerfile})
		}
	}
	g, _ := cmake.Discover(repoDir)
	for _, d := range g.Dirs {
		add(Unit{Dir: d, Name: path.Base(d), Kind: "cmake",
			Evidence: "CMake target defined in " + d + "/CMakeLists.txt"})
	}
	for _, d := range goPackageDirs(repoDir) {
		add(Unit{Dir: d, Name: path.Base(d), Kind: "go",
			Evidence: "Go package in " + d})
	}
	out := make([]Unit, 0, len(byDir))
	for _, u := range byDir {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

// Unmodeled filters units to those with no corresponding workspace.dsl
// element: no component/container path glob maps the unit's directory
// (resolveDir is fed prefix+dir, the same git-root bridging every consistency
// check uses) and no element id or name matches the unit. An element that
// exists but lacks paths counts as modeled — a binding gap is model-health
// territory, not absence (ADR-0021).
func Unmodeled(units []Unit, prefix string, resolveDir func(string) string, elementNames []string) []Unit {
	names := map[string]bool{}
	for _, n := range elementNames {
		if n != "" {
			names[normalize(n)] = true
		}
	}
	var out []Unit
	for _, u := range units {
		if resolveDir(prefix+u.Dir) != "" {
			continue
		}
		if names[normalize(u.Name)] || names[normalize(path.Base(u.Dir))] {
			continue
		}
		out = append(out, u)
	}
	return out
}

// goUnitSkip prunes dirs that hold no first-party Go units (mirrors the
// deploy/cmake prune sets; testdata is invisible to the Go toolchain anyway).
func goUnitSkip(name string) bool {
	switch name {
	case "vendor", "node_modules", "third_party", "testdata", "build", "dist", "target", "out":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// goPackageDirs returns every dir under repoDir containing at least one
// buildable (non-test) .go file, when repoDir is a Go module root. The root
// itself is excluded: it is the module, not a sub-unit.
func goPackageDirs(repoDir string) []string {
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err != nil {
		return nil
	}
	seen := map[string]bool{}
	_ = filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != repoDir && goUnitSkip(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(repoDir, filepath.Dir(p))
		if rel = filepath.ToSlash(rel); rel != "." {
			seen[rel] = true
		}
		return nil
	})
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// normalize folds the naming skew between detector names and DSL ids
// (foo_service vs foo-service), matching deploy's container normalization.
func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
