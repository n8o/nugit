// Wiring-coherence scan (ADR-0026): config.yml declares enforcement, but the
// artifacts that wire nugit into a repo — CI workflows, CLAUDE.md, skill
// files — can silently cancel or contradict it. The rules live in
// internal/wiring, because `nugit pr-render` runs the same scan at the
// reviewed ref (ADR-0041) and two copies of them would be the drift they are
// here to catch. This file is the working-tree adapter.
package doctor

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/wiring"
)

// wiringChecks builds the ADR-0026 advisory coherence checks from the CHECKOUT.
// cfg is the parsed (possibly default) config — the source of truth the wiring
// artifacts are compared against.
func wiringChecks(repoDir string, cfg config.Config) []Check {
	src := wiring.Source{
		ClaudeMD:  []string{"CLAUDE.md"},
		Skills:    skillFiles(repoDir),
		Workflows: workflowFiles(repoDir),
		Read: func(rel string) string {
			b, err := os.ReadFile(filepath.Join(repoDir, rel))
			if err != nil {
				return "" // tolerant: an unreadable file is simply not evidence
			}
			return string(b)
		},
	}
	rep := wiring.Scan(src, cfg)
	// All three are Advisory by construction HERE: a pre-flight informs the
	// human and never flips its own exit code (ADR-0026 rejected hard-failing).
	// The PR-time copies warn instead, which is what makes them land on someone
	// (ADR-0041) — doctor alone left a stale pin on the pilot for four weeks.
	return []Check{
		{Name: "CI fail-on matches config", OK: len(rep.WeakFailOn) == 0, Advisory: true, Detail: rep.FailOnDetail(cfg)},
		{Name: "install pins agree", OK: rep.PinsAgree(), Advisory: true, Detail: rep.PinDetail()},
		{Name: "skill docs match config", OK: len(rep.C4Contradictions) == 0, Advisory: true, Detail: rep.C4Detail()},
	}
}

// workflowFiles lists .github/workflows/*.yml|*.yaml, repo-relative, sorted.
func workflowFiles(repoDir string) []string {
	entries, err := os.ReadDir(filepath.Join(repoDir, ".github", "workflows"))
	if err != nil {
		return nil // tolerant: no workflows directory is fine
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yml", ".yaml":
			out = append(out, filepath.Join(".github", "workflows", e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// skillFiles lists .claude/skills/**/SKILL.md, repo-relative, sorted.
func skillFiles(repoDir string) []string {
	var out []string
	root := filepath.Join(repoDir, ".claude", "skills")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerant: skip unreadable subtrees
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			if rel, rerr := filepath.Rel(repoDir, p); rerr == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}
