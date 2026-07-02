// Package scaffold implements `nugit init`: it creates the .nugit/ tree for a
// repo, bootstrapping a first-pass C4 model from the Go import graph. It never
// clobbers existing files unless Force is set.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/n8o/nugit/internal/bootstrap"
	"github.com/n8o/nugit/internal/gitutil"
)

// Options configures an init run.
type Options struct {
	RepoDir       string
	Force         bool     // overwrite existing files
	NoModel       bool     // scaffold only; write a template workspace.dsl
	Mode          string   // c4 enforcement written to config: warn (default) | enforce
	Layout        string   // structural layout: container|toplevel|flat ("" = auto: Go import graph, else structural)
	ComponentDirs []string // override the container dir set for structural layout
}

// Result reports what init did.
type Result struct {
	Created       []string
	Skipped       []string
	Components    int
	Edges         int
	Mode          string
	ModelEmpty    bool   // no components found; wrote a template
	WroteModel    bool   // a bootstrapped (non-template) workspace.dsl was written
	Structural    bool   // components came from the directory layout (no edges)
	CMake         bool   // components + edges came from CMake target_link_libraries
	Backend       string // human label of the edged backend (CMake/Python imports/TypeScript)
	DSLCreated    bool   // a workspace.dsl was actually written (not skipped)
	PolyglotHint  bool   // a root go.mod was used but the layout suggests a polyglot repo
	HookInstalled bool   // a commit-msg hook was installed
}

// Run scaffolds .nugit/ under opt.RepoDir.
func Run(opt Options) (Result, error) {
	if opt.Mode == "" {
		opt.Mode = "warn"
	}
	if opt.Mode != "warn" && opt.Mode != "enforce" {
		return Result{}, fmt.Errorf("invalid -mode %q (want warn|enforce)", opt.Mode)
	}
	res := Result{Mode: opt.Mode}
	nugitDir := filepath.Join(opt.RepoDir, ".nugit")

	// Re-running on an existing .nugit is safe: writeFile tops up missing files
	// but never overwrites unless Force is set, so init is idempotent.
	for _, d := range []string{"architecture", "decisions", "specs", "lessons", "references"} {
		if err := os.MkdirAll(filepath.Join(nugitDir, d), 0o755); err != nil {
			return res, err
		}
	}

	var ferr error
	put := func(path, content string) {
		if ferr == nil {
			ferr = writeFile(&res, path, content, opt.Force)
		}
	}
	for _, d := range []string{"decisions", "specs", "lessons", "references"} {
		put(filepath.Join(nugitDir, d, ".gitkeep"), "")
	}
	put(filepath.Join(nugitDir, "config.yml"), configYML(opt.Mode))
	put(filepath.Join(nugitDir, "glossary.md"), glossaryMD())

	dslPath := filepath.Join(nugitDir, "architecture", "workspace.dsl")
	name := repoName(opt.RepoDir)
	switch {
	case opt.NoModel:
		put(dslPath, templateDSL(name))
	case opt.Layout == "cmake":
		g, err := bootstrap.DiscoverCMake(opt.RepoDir)
		if err != nil {
			return res, err
		}
		res.Components, res.Edges, res.CMake, res.Backend = len(g.Components), len(g.Edges), true, "CMake"
		writeModel(&res, put, dslPath, g, name)
	case opt.Layout == "python":
		g, err := bootstrap.DiscoverPython(opt.RepoDir)
		if err != nil {
			return res, err
		}
		res.Components, res.Edges, res.Backend = len(g.Components), len(g.Edges), "Python imports"
		writeModel(&res, put, dslPath, g, name)
	case opt.Layout == "ts":
		g, _, err := bootstrap.DiscoverTS(opt.RepoDir)
		if err != nil {
			return res, err
		}
		res.Components, res.Edges, res.Backend = len(g.Components), len(g.Edges), "TypeScript (dependency-cruiser)"
		writeModel(&res, put, dslPath, g, name)
	case opt.Layout != "":
		// Explicit structural layout (any codebase).
		g, err := bootstrap.DiscoverStructural(opt.RepoDir,
			bootstrap.StructuralOptions{Layout: opt.Layout, ContainerDirs: opt.ComponentDirs})
		if err != nil {
			return res, err
		}
		res.Components, res.Structural = len(g.Components), true
		writeModel(&res, put, dslPath, g, name)
	case hasGoMod(opt.RepoDir):
		// A Go module is rooted here: use the import graph (with edges), falling
		// back to structural only if it somehow finds nothing.
		g, err := bootstrap.Discover(opt.RepoDir)
		if err != nil {
			return res, err
		}
		sg, err := bootstrap.DiscoverStructural(opt.RepoDir,
			bootstrap.StructuralOptions{ContainerDirs: opt.ComponentDirs})
		if err != nil {
			return res, err
		}
		switch {
		case len(g.Components) == 0:
			// Empty Go model: prefer CMake (edged) over an edge-free structural
			// model, so a repo with an incidental root go.mod but a real CMake
			// architecture still gets enforceable edges.
			if bootstrap.DetectCMake(opt.RepoDir) {
				cg, err := bootstrap.DiscoverCMake(opt.RepoDir)
				if err != nil {
					return res, err
				}
				if len(cg.Components) > 0 {
					g, res.CMake = cg, true
				}
			}
			if !res.CMake && len(sg.Components) > 0 {
				g, res.Structural = sg, true
			}
		case len(sg.Components) > len(g.Components):
			// The directory layout has more structure than the Go model covers —
			// likely a polyglot repo with a root go.mod. Keep the Go model (it has
			// edges) but hint at -layout for a whole-repo view.
			res.PolyglotHint = true
		}
		res.Components, res.Edges = len(g.Components), len(g.Edges)
		writeModel(&res, put, dslPath, g, name)
	case bootstrap.DetectCMake(opt.RepoDir) || bootstrap.DetectPython(opt.RepoDir) || bootstrap.DetectTS(opt.RepoDir):
		// No Go module, but a buildsystem/language we can derive edges from
		// (CMake -> Python -> TS, in that order). An edged model is enforceable.
		g, backend, err := autoEdged(opt.RepoDir)
		if err != nil {
			return res, err
		}
		if len(g.Components) > 0 {
			res.Components, res.Edges, res.Backend, res.CMake = len(g.Components), len(g.Edges), backend, backend == "CMake"
			writeModel(&res, put, dslPath, g, name)
		} else {
			sg, err := bootstrap.DiscoverStructural(opt.RepoDir, bootstrap.StructuralOptions{ContainerDirs: opt.ComponentDirs})
			if err != nil {
				return res, err
			}
			res.Components, res.Structural = len(sg.Components), true
			writeModel(&res, put, dslPath, sg, name)
		}
	default:
		// No Go module / CMake rooted here (polyglot / non-Go / monorepo root): a
		// structural directory-layout model is the right default — incidental Go
		// packages scattered in subtrees would otherwise yield a misleading
		// Go-only model that ignores the rest of the repo.
		g, err := bootstrap.DiscoverStructural(opt.RepoDir,
			bootstrap.StructuralOptions{ContainerDirs: opt.ComponentDirs})
		if err != nil {
			return res, err
		}
		res.Components, res.Structural = len(g.Components), true
		writeModel(&res, put, dslPath, g, name)
	}
	if ferr != nil {
		return res, ferr
	}
	res.DSLCreated = contains(res.Created, dslPath)
	res.WroteModel = !opt.NoModel && !res.ModelEmpty && res.DSLCreated

	if err := ensureGitignore(&res, opt.RepoDir); err != nil {
		return res, err
	}
	installCommitMsgHook(&res, opt.RepoDir, opt.Force)
	return res, nil
}

// installCommitMsgHook writes a commit-msg hook that validates trailer blocks
// (it skips if nugit isn't on PATH, so it never breaks commits). A pre-existing
// hook is preserved unless Force is set.
func installCommitMsgHook(res *Result, repoDir string, force bool) {
	hooks := gitutil.Repo{Dir: repoDir}.HooksDir()
	if hooks == "" {
		return // not a git repo
	}
	path := filepath.Join(hooks, "commit-msg")
	if _, err := os.Stat(path); err == nil && !force {
		res.Skipped = append(res.Skipped, path)
		return
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return
	}
	// Git runs hooks with cwd = git toplevel; when the nugit root is nested, embed
	// its prefix so the hook loads the right .nugit/config.yml (not the toplevel's).
	cArg := ""
	if prefix := strings.TrimSuffix(gitutil.Repo{Dir: repoDir}.Prefix(), "/"); prefix != "" {
		cArg = " -C \"" + prefix + "\""
	}
	script := "#!/bin/sh\n" +
		"# installed by `nugit init` — validates the commit-trailer block (§6.1)\n" +
		"command -v nugit >/dev/null 2>&1 || exit 0\n" +
		"exec nugit hook commit-msg" + cArg + " \"$1\"\n"
	if os.WriteFile(path, []byte(script), 0o755) == nil {
		res.Created = append(res.Created, path)
		res.HookInstalled = true
	}
}

// writeFile writes path unless it exists and !force (then records a skip). It
// returns an error on write failure so a partial init is never reported as
// success (which would render falsely green on an absent model).
func writeFile(res *Result, path, content string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		res.Skipped = append(res.Skipped, path)
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	res.Created = append(res.Created, path)
	return nil
}

// ensureGitignore appends the nugit ignore lines if absent, matching WHOLE lines
// (not substrings, which could false-positive on a comment or a longer path).
func ensureGitignore(res *Result, repoDir string) error {
	path := filepath.Join(repoDir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	text := string(existing)
	have := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, line := range []string{"**/.nugit/.cache/", ".nugit-local/"} {
		if !have[line] {
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "\n# nugit (rebuildable index + per-agent ephemeral memory)\n" + strings.Join(add, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	res.Created = append(res.Created, path)
	return nil
}

// writeModel writes the bootstrapped DSL, or a template when no components were
// found (setting ModelEmpty).
func writeModel(res *Result, put func(string, string), dslPath string, g bootstrap.Graph, name string) {
	if len(g.Components) == 0 {
		res.ModelEmpty = true
		put(dslPath, templateDSL(name))
		return
	}
	put(dslPath, bootstrap.GenerateDSL(g, name))
}

func hasGoMod(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}

// autoEdged tries each non-Go edged backend in priority order (CMake, Python,
// TypeScript) and returns the first that yields components, with a human label.
func autoEdged(repoDir string) (bootstrap.Graph, string, error) {
	if bootstrap.DetectCMake(repoDir) {
		g, err := bootstrap.DiscoverCMake(repoDir)
		if err != nil {
			return bootstrap.Graph{}, "", err
		}
		if len(g.Components) > 0 {
			return g, "CMake", nil
		}
	}
	if bootstrap.DetectPython(repoDir) {
		g, err := bootstrap.DiscoverPython(repoDir)
		if err != nil {
			return bootstrap.Graph{}, "", err
		}
		if len(g.Components) > 0 {
			return g, "Python imports", nil
		}
	}
	if bootstrap.DetectTS(repoDir) {
		g, _, err := bootstrap.DiscoverTS(repoDir)
		if err != nil {
			return bootstrap.Graph{}, "", err
		}
		if len(g.Components) > 0 {
			return g, "TypeScript (dependency-cruiser)", nil
		}
	}
	return bootstrap.Graph{}, "", nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func repoName(repoDir string) string {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		abs = repoDir
	}
	base := filepath.Base(abs)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "workspace"
	}
	return base
}
