package bootstrap

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/n8o/nugit/internal/gitutil"
)

// StructuralOptions controls language-agnostic component discovery.
type StructuralOptions struct {
	// Layout: "container" (default — descend into apps/libs/... container dirs),
	// "toplevel" (one component per top-level dir), or "flat" (one root component).
	Layout string
	// ContainerDirs overrides the default container set for "container" layout.
	ContainerDirs []string
}

// DefaultContainerDirs are the conventional monorepo grouping directories: each
// of their immediate children becomes a component.
func DefaultContainerDirs() []string {
	return []string{"apps", "libs", "services", "packages", "modules", "plugins"}
}

// structuralSkip skips build output / vendored dirs in addition to skipDir, so a
// directory-layout model isn't polluted with non-first-party noise.
func structuralSkip(name string) bool {
	switch name {
	case "third_party", "build", "out", "dist", "bin", "target", "obj":
		return true
	}
	return skipDir(name)
}

// DiscoverStructural builds a component-per-directory model for ANY codebase,
// with NO edges (there is no language-neutral way to derive dependencies — that
// is the per-language analyzer's job, P2). Globs are git-root-relative.
func DiscoverStructural(rootDir string, opt StructuralOptions) (Graph, error) {
	if opt.Layout == "" {
		opt.Layout = "container"
	}
	prefix := gitutil.Repo{Dir: rootDir}.Prefix()

	containers := map[string]bool{}
	src := opt.ContainerDirs
	if len(src) == 0 {
		src = DefaultContainerDirs()
	}
	for _, c := range src {
		containers[c] = true
	}

	var compDirs []string // nugit-root-relative dirs that become components
	switch opt.Layout {
	case "flat":
		compDirs = []string{"."}
	case "toplevel":
		compDirs = append(compDirs, readDirs(rootDir, "", nil)...)
	default: // container
		// An explicitly-requested container name (e.g. "target") must not be
		// dropped by the build-dir denylist — pass containers as a keep set.
		for _, top := range readDirs(rootDir, "", containers) {
			if containers[top] {
				children := readDirs(rootDir, top, nil)
				if len(children) == 0 {
					compDirs = append(compDirs, top) // empty container → itself
					continue
				}
				for _, ch := range children {
					compDirs = append(compDirs, top+"/"+ch)
				}
			} else {
				compDirs = append(compDirs, top)
			}
		}
	}
	sort.Strings(compDirs)
	if len(compDirs) == 0 {
		return Graph{Structural: true}, nil
	}

	idOf := assignIDs(compDirs)
	g := Graph{Structural: true}
	for _, d := range compDirs {
		glob := gitRootGlob(prefix, d)
		if opt.Layout == "flat" {
			glob = prefix + "**"
		}
		g.Components = append(g.Components, Component{ID: idOf[d], Name: lastSeg(d), Dir: d, Glob: glob})
	}
	return g, nil
}

// readDirs returns the immediate child directory names of rootDir/sub, sorted,
// skipping non-first-party dirs except names in keep (explicitly-requested
// containers that must survive the build-dir denylist).
func readDirs(rootDir, sub string, keep map[string]bool) []string {
	entries, err := os.ReadDir(filepath.Join(rootDir, sub))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && (keep[e.Name()] || !structuralSkip(e.Name())) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
