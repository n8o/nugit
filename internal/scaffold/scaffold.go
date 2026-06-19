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
)

// Options configures an init run.
type Options struct {
	RepoDir string
	Force   bool   // overwrite existing files
	NoModel bool   // scaffold only; write a template workspace.dsl
	Mode    string // c4 enforcement written to config: warn (default) | enforce
}

// Result reports what init did.
type Result struct {
	Created    []string
	Skipped    []string
	Components int
	Edges      int
	Mode       string
	ModelEmpty bool // no Go packages found; wrote a template
	WroteModel bool // a bootstrapped (non-template) workspace.dsl was written
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
	for _, d := range []string{"architecture", "decisions", "specs", "lessons"} {
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
	for _, d := range []string{"decisions", "specs", "lessons"} {
		put(filepath.Join(nugitDir, d, ".gitkeep"), "")
	}
	put(filepath.Join(nugitDir, "config.yml"), configYML(opt.Mode))
	put(filepath.Join(nugitDir, "glossary.md"), glossaryMD())

	dslPath := filepath.Join(nugitDir, "architecture", "workspace.dsl")
	name := repoName(opt.RepoDir)
	if opt.NoModel {
		put(dslPath, templateDSL(name))
	} else {
		g, err := bootstrap.Discover(opt.RepoDir)
		if err != nil {
			return res, err
		}
		res.Components, res.Edges = len(g.Components), len(g.Edges)
		if len(g.Components) == 0 {
			res.ModelEmpty = true
			put(dslPath, templateDSL(name))
		} else {
			put(dslPath, bootstrap.GenerateDSL(g, name))
		}
	}
	if ferr != nil {
		return res, ferr
	}
	res.WroteModel = !opt.NoModel && !res.ModelEmpty && contains(res.Created, dslPath)

	if err := ensureGitignore(&res, opt.RepoDir); err != nil {
		return res, err
	}
	return res, nil
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
