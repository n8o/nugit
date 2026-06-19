// Package cmake derives a component dependency graph from a CMake project
// WITHOUT a configure step, by statically parsing target_link_libraries.
//
// CMake is C++'s dependency system: `target_link_libraries(foo PRIVATE bar)` is
// an explicit, reliable "foo depends on bar" edge at C4-component altitude — far
// better than scanning preprocessor #includes. Components are the directories
// that DEFINE targets; edges are first-party link deps mapped to their defining
// dirs. The CMake File API is the accurate upgrade (resolves variable/conditional
// edges this static parse can't); this zero-config path needs no build env.
package cmake

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Graph is a directory-level component graph (repo/nugit-root-relative dirs).
type Graph struct {
	Dirs  []string    // sorted: dirs that define ≥1 CMake target
	Edges [][2]string // sorted, deduped, no self-edges: [srcDir, dstDir]
}

// File is one CMakeLists.txt with its repo-relative directory — the unit the
// parser consumes, so the file source can be disk (init) or a git ref (check).
type File struct {
	Dir  string
	Text string
}

func skip(name string) bool {
	switch name {
	case ".git", ".nugit", "vendor", "node_modules", "third_party":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

var (
	addTarget   = regexp.MustCompile(`\badd_(?:library|executable)\s*\(`)
	tllOpen     = regexp.MustCompile(`\btarget_link_libraries\s*\(`)
	identRE     = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	linkKeyword = map[string]bool{
		"PRIVATE": true, "PUBLIC": true, "INTERFACE": true,
		"LINK_PRIVATE": true, "LINK_PUBLIC": true, "LINK_INTERFACE_LIBRARIES": true,
		"debug": true, "optimized": true, "general": true,
	}
	// keywords that mark a non-first-party target declaration we must not register.
	notFirstParty = map[string]bool{"ALIAS": true, "IMPORTED": true}
)

// Discover statically parses every CMakeLists.txt under rootDir (on disk) and
// returns the directory-level component graph. *.cmake helper modules are
// ignored — targets there are usually macros, not component definitions.
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
		if d.Name() != "CMakeLists.txt" {
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

// DiscoverFiles parses an explicit set of CMakeLists files (used to read at a git
// ref). Comments and string literals are blanked before parsing so a documented
// or commented-out call never produces a phantom target/edge, and a `#`/`)` in a
// comment can't corrupt parenthesis balancing.
func DiscoverFiles(files []File) Graph {
	type san struct {
		dir  string
		text string
	}
	sans := make([]san, 0, len(files))
	for _, f := range files {
		sans = append(sans, san{dir: f.Dir, text: sanitize(f.Text)})
	}

	// Pass 1: target name -> defining directory (first wins).
	targetDir := map[string]string{}
	for _, f := range sans {
		for _, args := range calls(f.text, addTarget) {
			toks := strings.Fields(args)
			if len(toks) == 0 {
				continue
			}
			name := strings.Trim(toks[0], `"`)
			if strings.Contains(name, "::") || !identRE.MatchString(name) {
				continue // namespaced / external / variable target
			}
			if hasKeyword(toks[1:], notFirstParty) {
				continue // ALIAS / IMPORTED — not a first-party source component
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
	for _, f := range sans {
		for _, args := range calls(f.text, tllOpen) {
			toks := strings.Fields(args)
			if len(toks) == 0 {
				continue
			}
			srcDir, ok := targetDir[strings.Trim(toks[0], `"`)]
			if !ok {
				continue
			}
			for _, dep := range toks[1:] {
				d := strings.Trim(dep, `"`)
				if linkKeyword[d] || !identRE.MatchString(d) {
					continue // keyword / ${var} / $<genex> / namespaced external
				}
				dstDir, ok := targetDir[d]
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
	return g
}

// Detect reports whether rootDir looks like a CMake project.
func Detect(rootDir string) bool {
	_, err := os.Stat(filepath.Join(rootDir, "CMakeLists.txt"))
	return err == nil
}

func hasKeyword(toks []string, set map[string]bool) bool {
	for _, t := range toks {
		if set[strings.ToUpper(t)] {
			return true
		}
	}
	return false
}

// calls yields the argument text of each open(...) call with balanced parens
// (calls may span many lines). Comments/strings must already be blanked.
func calls(text string, open *regexp.Regexp) []string {
	var out []string
	idx := 0
	for {
		loc := open.FindStringIndex(text[idx:])
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

// sanitize blanks CMake comments (# line, #[[ ]] bracket) and double-quoted
// string contents with spaces, preserving newlines and byte offsets, so the
// parser only sees real code.
func sanitize(s string) string {
	out := []byte(s)
	blank := func(from, to int) {
		for k := from; k < to && k < len(out); k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	i, n := 0, len(s)
	for i < n {
		switch {
		case s[i] == '#' && i+2 < n && s[i+1] == '[' && s[i+2] == '[':
			j := i + 3
			for j+1 < n && !(s[j] == ']' && s[j+1] == ']') {
				j++
			}
			end := j + 2
			if end > n {
				end = n
			}
			blank(i, end)
			i = end
		case s[i] == '#':
			j := i
			for j < n && s[j] != '\n' {
				j++
			}
			blank(i, j)
			i = j
		case s[i] == '"':
			j := i + 1
			for j < n && s[j] != '"' {
				if s[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			end := j + 1
			if end > n {
				end = n
			}
			blank(i, end)
			i = end
		default:
			i++
		}
	}
	return string(out)
}
