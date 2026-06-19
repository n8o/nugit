// Package pyimports derives a directory-level dependency graph from Python
// import statements, in pure Go (no python3, no install). Components are package
// directories (dirs containing .py files); edges are cross-package imports.
// Approximate (resolves absolute imports against the repo root and src/, relative
// imports against the file's package) — the accurate path is a real AST/resolver,
// but this is honest and zero-dependency.
package pyimports

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Graph is a directory-level component graph (repo/nugit-root-relative dirs).
type Graph struct {
	Dirs  []string
	Edges [][2]string
}

// File is one .py file with its repo-relative directory.
type File struct {
	Dir  string // package dir (the file's directory)
	Text string
}

func skip(name string) bool {
	switch name {
	case ".git", ".nugit", "vendor", "node_modules", "third_party",
		"__pycache__", "venv", ".venv", "site-packages", "build", "dist":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// Detect reports whether rootDir looks like a Python project.
func Detect(rootDir string) bool {
	for _, f := range []string{"pyproject.toml", "setup.py", "setup.cfg"} {
		if _, err := os.Stat(filepath.Join(rootDir, f)); err == nil {
			return true
		}
	}
	// else: any .py outside skipped dirs
	found := false
	filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.IsDir() {
			if p != rootDir && skip(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".py") {
			found = true
			return fs.SkipDir
		}
		return nil
	})
	return found
}

// Discover walks rootDir for .py files and returns the package dir graph.
func Discover(rootDir string) (Graph, error) {
	var files []File
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
		if !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, filepath.Dir(p))
		files = append(files, File{Dir: filepath.ToSlash(rel), Text: string(b)})
		return nil
	})
	if err != nil {
		return Graph{}, err
	}
	return DiscoverFiles(files), nil
}

var (
	importRE = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+([A-Za-z0-9_.]+)`)
	fromRE   = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+(\.*)([A-Za-z0-9_.]*)[ \t]+import\b`)
)

// DiscoverFiles builds the graph from an explicit set of .py files (for reading
// at a git ref).
func DiscoverFiles(files []File) Graph {
	pkg := map[string]bool{} // package dirs
	for _, f := range files {
		pkg[f.Dir] = true
	}
	edgeSet := map[[2]string]bool{}
	for _, f := range files {
		for _, t := range targets(f, pkg) {
			if t != "" && t != f.Dir {
				edgeSet[[2]string{f.Dir, t}] = true
			}
		}
	}
	var g Graph
	for d := range pkg {
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

// targets resolves the import statements in a file to package dirs.
func targets(f File, pkg map[string]bool) []string {
	var out []string
	resolve := func(mod string) {
		if d := resolveAbs(mod, pkg); d != "" {
			out = append(out, d)
		}
	}
	for _, m := range importRE.FindAllStringSubmatch(f.Text, -1) {
		resolve(m[1])
	}
	for _, m := range fromRE.FindAllStringSubmatch(f.Text, -1) {
		dots, mod := m[1], m[2]
		if dots == "" {
			resolve(mod)
			continue
		}
		// relative: go up len(dots)-1 dirs from the file's package, then into mod.
		base := f.Dir
		for i := 1; i < len(dots); i++ {
			base = parent(base)
		}
		target := base
		if mod != "" {
			target = path.Join(base, strings.ReplaceAll(mod, ".", "/"))
		}
		if t := longestPkg(target, pkg); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// resolveAbs maps a dotted absolute module to a package dir, trying the repo root
// and a src/ root, longest matching package prefix first.
func resolveAbs(mod string, pkg map[string]bool) string {
	rel := strings.ReplaceAll(mod, ".", "/")
	for _, root := range []string{"", "src/"} {
		if t := longestPkg(root+rel, pkg); t != "" {
			return t
		}
	}
	return ""
}

// longestPkg returns the longest ancestor of dir (including dir) that is a known
// package dir.
func longestPkg(dir string, pkg map[string]bool) string {
	dir = path.Clean(dir)
	for dir != "" && dir != "." && dir != "/" {
		if pkg[dir] {
			return dir
		}
		dir = parent(dir)
	}
	if pkg["."] {
		return "."
	}
	return ""
}

func parent(dir string) string {
	if i := strings.LastIndexByte(dir, '/'); i >= 0 {
		return dir[:i]
	}
	return "."
}
