// Package tsdeps derives a directory-level dependency graph for TypeScript/JS by
// shelling out to dependency-cruiser (MIT), which resolves the real import graph.
// When dependency-cruiser is not installed, Discover reports available=false so
// the caller can surface an "unenforced" info finding rather than silently pass.
package tsdeps

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Graph is a directory-level component graph (repo/nugit-root-relative dirs).
type Graph struct {
	Dirs  []string
	Edges [][2]string
}

// Detect reports whether rootDir looks like a TS/JS project.
func Detect(rootDir string) bool {
	if _, err := os.Stat(filepath.Join(rootDir, "package.json")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(rootDir, "tsconfig.json")); err == nil {
		return true
	}
	return hasExt(rootDir, ".ts", ".tsx", ".js", ".jsx")
}

// Available reports whether dependency-cruiser is on PATH.
func Available() bool {
	_, err := exec.LookPath("depcruise")
	return err == nil
}

// Discover runs dependency-cruiser over rootDir and returns the dir graph. The
// bool is whether dependency-cruiser was available (false -> graph is empty and
// the caller should surface that TS edges are unenforced).
func Discover(rootDir string) (Graph, bool, error) {
	if !Available() {
		return Graph{}, false, nil
	}
	target := "."
	if _, err := os.Stat(filepath.Join(rootDir, "src")); err == nil {
		target = "src"
	}
	cmd := exec.Command("depcruise", "--no-config", "--output-type", "json", target)
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		// depcruise exits non-zero when it reports violations; the JSON is still
		// on stdout, so try to parse it anyway.
		if len(out) == 0 {
			return Graph{}, true, err
		}
	}
	return ParseDepcruise(out), true, nil
}

// ParseDepcruise turns dependency-cruiser JSON into a directory graph. Exported
// for testing without the binary.
func ParseDepcruise(b []byte) Graph {
	var dc struct {
		Modules []struct {
			Source       string `json:"source"`
			Dependencies []struct {
				Resolved        string `json:"resolved"`
				CoreModule      bool   `json:"coreModule"`
				CouldNotResolve bool   `json:"couldNotResolve"`
			} `json:"dependencies"`
		} `json:"modules"`
	}
	if json.Unmarshal(b, &dc) != nil {
		return Graph{}
	}
	dirSet := map[string]bool{}
	edgeSet := map[[2]string]bool{}
	for _, m := range dc.Modules {
		if external(m.Source) {
			continue
		}
		src := dir(m.Source)
		dirSet[src] = true
		for _, d := range m.Dependencies {
			if d.CoreModule || d.CouldNotResolve || external(d.Resolved) {
				continue
			}
			dst := dir(d.Resolved)
			dirSet[dst] = true
			if dst != src {
				edgeSet[[2]string{src, dst}] = true
			}
		}
	}
	var g Graph
	for d := range dirSet {
		g.Dirs = append(g.Dirs, d)
	}
	sort.Strings(g.Dirs)
	for e := range edgeSet {
		g.Edges = append(g.Edges, e)
	}
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i][0] != g.Edges[j][0] {
			return g.Edges[i][0] < g.Edges[j][0]
		}
		return g.Edges[i][1] < g.Edges[j][1]
	})
	return g
}

func external(p string) bool {
	return p == "" || strings.HasPrefix(p, "node_modules/") || strings.Contains(p, "/node_modules/")
}

func dir(p string) string {
	d := path.Dir(filepath.ToSlash(p))
	if d == "" {
		return "."
	}
	return d
}

func hasExt(rootDir string, exts ...string) bool {
	found := false
	filepath.WalkDir(rootDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if p != rootDir && (n == "node_modules" || n == ".git" || strings.HasPrefix(n, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		for _, e := range exts {
			if strings.HasSuffix(d.Name(), e) {
				found = true
				return filepath.SkipDir
			}
		}
		return nil
	})
	return found
}
