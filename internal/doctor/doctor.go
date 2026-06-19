// Package doctor is a fast pre-flight that verifies a nugit adoption is healthy:
// config parses, the C4 model parses and is non-empty, the commit-msg hook is
// installed, a language backend is detected, and the knowledge store loads. It
// answers "is nugit set up correctly here?" without running a full pr-render.
package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/n8o/nugit/internal/bootstrap"
	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
)

// Check is one health probe.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Report is the full pre-flight result.
type Report struct {
	Checks []Check
}

// AllOK reports whether every check passed.
func (r Report) AllOK() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// Run probes the nugit adoption rooted at repoDir.
func Run(repoDir string) Report {
	var r Report
	add := func(name string, ok bool, detail string) {
		r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: detail})
	}

	cfg, err := config.Load(repoDir)
	add("config.yml parses", err == nil, errOr(err, "c4.mode="+cfg.C4.Mode))

	dslPath := cfg.C4.DSL
	if dslPath == "" {
		dslPath = ".nugit/architecture/workspace.dsl"
	}
	src, derr := os.ReadFile(filepath.Join(repoDir, dslPath))
	m := c4.Parse(string(src))
	add("workspace.dsl parses with components", derr == nil && len(m.Components) > 0,
		fmt.Sprintf("%d component(s), %d relationship(s)", len(m.Components), len(m.Relationships)))

	hooks := gitutil.Repo{Dir: repoDir}.HooksDir()
	hookInstalled := false
	if hooks != "" {
		_, e := os.Stat(filepath.Join(hooks, "commit-msg"))
		hookInstalled = e == nil
	}
	add("commit-msg hook installed", hookInstalled, hookDetail(hookInstalled))

	add("language backend detected", backend(repoDir) != "", backend(repoDir))

	objs, kerr := knowledge.Load(repoDir)
	add("knowledge store loads", kerr == nil, fmt.Sprintf("%d object(s)", len(objs)))

	return r
}

// backend names the analyzer nugit init would use here (matches the auto-detect order).
func backend(repoDir string) string {
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		return "Go (import graph)"
	}
	switch {
	case bootstrap.DetectCMake(repoDir):
		return "C++ (CMake)"
	case bootstrap.DetectPython(repoDir):
		return "Python (imports)"
	case bootstrap.DetectTS(repoDir):
		return "TypeScript (dependency-cruiser)"
	default:
		return "structural (directory layout)"
	}
}

func errOr(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

func hookDetail(ok bool) string {
	if ok {
		return "validates trailer blocks on commit"
	}
	return "run `nugit init` to install"
}
