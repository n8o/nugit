// Package cmake derives a component dependency graph from a CMake project
// WITHOUT a configure step, by statically parsing target_link_libraries.
//
// CMake is C++'s dependency system: `target_link_libraries(foo PRIVATE bar)` is
// an explicit, reliable "foo depends on bar" edge at C4-component altitude — far
// better than scanning preprocessor #includes. Components are the directories
// that DEFINE targets; edges are first-party link deps mapped to their defining
// dirs (so the altitude matches nugit's directory-based components). The CMake
// File API is the accurate upgrade (resolves variable/conditional edges this
// static parse can't); this zero-config path needs no build environment.
package cmake

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Graph is a directory-level component graph (repo-relative dirs).
type Graph struct {
	Dirs  []string    // sorted: dirs that define ≥1 CMake target
	Edges [][2]string // sorted, deduped, no self-edges: [srcDir, dstDir]
}

func skip(name string) bool {
	switch name {
	case ".git", ".nugit", "vendor", "node_modules", "third_party",
		"build", "out", "dist", "bin", "target", "obj":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

var (
	addTarget   = regexp.MustCompile(`\badd_(?:library|executable)\s*\(\s*([A-Za-z0-9_]+)`)
	tllOpen     = regexp.MustCompile(`\btarget_link_libraries\s*\(`)
	identRE     = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	linkKeyword = map[string]bool{
		"PRIVATE": true, "PUBLIC": true, "INTERFACE": true,
		"LINK_PRIVATE": true, "LINK_PUBLIC": true, "LINK_INTERFACE_LIBRARIES": true,
		"debug": true, "optimized": true, "general": true,
	}
)

// Discover statically parses every CMakeLists.txt / *.cmake under rootDir and
// returns the directory-level component graph.
func Discover(rootDir string) (Graph, error) {
	type file struct {
		dir  string // repo-relative dir of the CMakeLists
		text string
	}
	var files []file
	err := filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != rootDir && skip(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "CMakeLists.txt" && !strings.HasSuffix(d.Name(), ".cmake") {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, filepath.Dir(p))
		files = append(files, file{dir: filepath.ToSlash(rel), text: string(b)})
		return nil
	})
	if err != nil {
		return Graph{}, err
	}

	// Pass 1: target name -> defining directory.
	targetDir := map[string]string{}
	for _, f := range files {
		for _, m := range addTarget.FindAllStringSubmatch(f.text, -1) {
			name := m[1]
			if strings.EqualFold(name, "ALIAS") {
				continue
			}
			if _, dup := targetDir[name]; !dup {
				targetDir[name] = f.dir
			}
		}
	}

	// Pass 2: edges from target_link_libraries, mapped to defining dirs.
	dirSet := map[string]bool{}
	for _, d := range targetDir {
		dirSet[d] = true
	}
	edgeSet := map[[2]string]bool{}
	for _, f := range files {
		for _, args := range linkCalls(f.text) {
			toks := strings.Fields(args)
			if len(toks) == 0 {
				continue
			}
			srcDir, ok := targetDir[toks[0]]
			if !ok {
				continue // links onto a non-first-party / unknown target
			}
			for _, dep := range toks[1:] {
				if linkKeyword[dep] || !identRE.MatchString(dep) {
					continue // keyword, ${var}, $<genex>, namespaced::external
				}
				dstDir, ok := targetDir[dep]
				if !ok || dstDir == srcDir {
					continue
				}
				edgeSet[[2]string{srcDir, dstDir}] = true
			}
		}
	}

	g := Graph{}
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
	return g, nil
}

// Detect reports whether rootDir looks like a CMake project.
func Detect(rootDir string) bool {
	_, err := os.Stat(filepath.Join(rootDir, "CMakeLists.txt"))
	return err == nil
}

// linkCalls yields the argument text of each target_link_libraries(...) call,
// with balanced parentheses (calls may span many lines).
func linkCalls(text string) []string {
	var out []string
	idx := 0
	for {
		loc := tllOpen.FindStringIndex(text[idx:])
		if loc == nil {
			return out
		}
		start := idx + loc[1] // just after '('
		depth, j := 1, start
		for j < len(text) && depth > 0 {
			switch text[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			j++
		}
		out = append(out, text[start:j-1])
		idx = j
	}
}
