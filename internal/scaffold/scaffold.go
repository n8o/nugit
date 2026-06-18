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

	if st, err := os.Stat(nugitDir); err == nil && st.IsDir() && !opt.Force {
		// .nugit exists: top up missing files but never overwrite (idempotent).
		// Continue with writeIfAbsent semantics rather than erroring, so re-running
		// init on a partially-set-up repo is safe.
	}

	for _, d := range []string{"architecture", "decisions", "specs", "lessons"} {
		if err := os.MkdirAll(filepath.Join(nugitDir, d), 0o755); err != nil {
			return res, err
		}
	}
	for _, d := range []string{"decisions", "specs", "lessons"} {
		writeFile(&res, filepath.Join(nugitDir, d, ".gitkeep"), "", opt.Force)
	}
	writeFile(&res, filepath.Join(nugitDir, "config.yml"), configYML(opt.Mode), opt.Force)
	writeFile(&res, filepath.Join(nugitDir, "glossary.md"), glossaryMD(), opt.Force)

	dslPath := filepath.Join(nugitDir, "architecture", "workspace.dsl")
	name := repoName(opt.RepoDir)
	if opt.NoModel {
		writeFile(&res, dslPath, templateDSL(name), opt.Force)
	} else {
		g, err := bootstrap.Discover(opt.RepoDir)
		if err != nil {
			return res, err
		}
		res.Components, res.Edges = len(g.Components), len(g.Edges)
		if len(g.Components) == 0 {
			res.ModelEmpty = true
			writeFile(&res, dslPath, templateDSL(name), opt.Force)
		} else {
			writeFile(&res, dslPath, bootstrap.GenerateDSL(g, name), opt.Force)
		}
	}

	ensureGitignore(&res, opt.RepoDir)
	return res, nil
}

// writeFile writes path unless it exists and !force (then records a skip).
func writeFile(res *Result, path, content string, force bool) {
	rel := path
	if _, err := os.Stat(path); err == nil && !force {
		res.Skipped = append(res.Skipped, rel)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err == nil {
		res.Created = append(res.Created, rel)
	}
}

// ensureGitignore appends the nugit ignore lines if absent.
func ensureGitignore(res *Result, repoDir string) {
	path := filepath.Join(repoDir, ".gitignore")
	want := []string{"**/.nugit/.cache/", ".nugit-local/"}
	existing, _ := os.ReadFile(path)
	text := string(existing)
	var add []string
	for _, line := range want {
		if !strings.Contains(text, line) {
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "\n# nugit (rebuildable index + per-agent ephemeral memory)\n" + strings.Join(add, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err == nil {
		res.Created = append(res.Created, path)
	}
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
