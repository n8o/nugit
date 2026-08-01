// Wiring-coherence scan (ADR-0026): config.yml declares enforcement, but the
// artifacts that wire nugit into a repo — CI workflows, CLAUDE.md, skill
// files — can silently cancel or contradict it (a workflow pinned to
// `-fail-on none` under an enforce config, install pins that disagree, skill
// prose asserting a stale c4.mode). These advisory checks make such drift
// visible. Everything here is a tolerant regex scan, never a YAML parse:
// unreadable or oddly-formatted files are skipped, never a doctor failure,
// and the checks never gate the exit code.
package doctor

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/config"
)

var (
	// wiringFailOnRE matches both CLI style (`-fail-on none`) and action-input
	// style (`fail-on: none`). config.yml's own `fail_on` (underscore) does not
	// match by construction.
	wiringFailOnRE = regexp.MustCompile(`fail-on[:=\s]+["']?(none|warn|fail)\b`)
	// wiringPinRE matches install/uses pins in any spelling: `nugit@main`,
	// `n8o/nugit@v0.3.0`, `github.com/n8o/nugit/cmd/nugit@latest`.
	wiringPinRE = regexp.MustCompile(`\bnugit@([A-Za-z0-9._-]+)`)
	// wiringC4ModeRE matches a config or prose assertion like `c4.mode: warn`.
	wiringC4ModeRE = regexp.MustCompile(`c4\.mode:\s*["']?(warn|enforce)\b`)
)

// wiringChecks builds the ADR-0026 advisory coherence checks. cfg is the
// parsed (possibly default) config — the source of truth the wiring artifacts
// are compared against.
func wiringChecks(repoDir string, cfg config.Config) []Check {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(repoDir, rel))
		if err != nil {
			return "" // tolerant: an unreadable file is simply not evidence
		}
		return string(b)
	}
	workflows := workflowFiles(repoDir)
	skills := skillFiles(repoDir)

	// (a) a workflow running nugit with a weaker fail-on than config declares.
	var weak []string
	sawFailOn := false
	for _, rel := range workflows {
		src := read(rel)
		seen := map[string]bool{}
		for _, m := range wiringFailOnRE.FindAllStringSubmatch(src, -1) {
			sawFailOn = true
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if config.FailOnRank(m[1]) < config.FailOnRank(cfg.PRRender.FailOn) {
				weak = append(weak, fmt.Sprintf("%s runs fail-on %s but config.yml says %s",
					rel, m[1], cfg.PRRender.FailOn))
			}
		}
	}
	var failOnDetail string
	switch {
	case len(weak) > 0:
		failOnDetail = strings.Join(weak, "; ") + " — enforcement is weaker in CI than the repo declares"
	case !sawFailOn:
		failOnDetail = "no fail-on found under .github/workflows"
	default:
		failOnDetail = fmt.Sprintf("no workflow weakens pr_render.fail_on: %s", cfg.PRRender.FailOn)
	}

	// (b) install pins agree across CLAUDE.md, skill files, and workflows.
	pins := map[string][]string{} // ref -> files that pin it
	scanPins := func(rel string) {
		seen := map[string]bool{}
		for _, m := range wiringPinRE.FindAllStringSubmatch(read(rel), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			pins[m[1]] = append(pins[m[1]], rel)
		}
	}
	scanPins("CLAUDE.md")
	for _, rel := range skills {
		scanPins(rel)
	}
	for _, rel := range workflows {
		scanPins(rel)
	}
	refs := make([]string, 0, len(pins))
	for ref := range pins {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	var pinDetail string
	switch len(refs) {
	case 0:
		pinDetail = "no nugit@<ref> pins found"
	case 1:
		pinDetail = fmt.Sprintf("all pins agree on @%s (%s)", refs[0], strings.Join(pins[refs[0]], ", "))
	default:
		var parts []string
		for _, ref := range refs {
			parts = append(parts, fmt.Sprintf("@%s (%s)", ref, strings.Join(pins[ref], ", ")))
		}
		pinDetail = fmt.Sprintf("%d different nugit pins: %s — align them so every surface runs the same version",
			len(refs), strings.Join(parts, ", "))
	}

	// (c) a skill file asserting a c4.mode that contradicts config.yml.
	var contradictions []string
	for _, rel := range skills {
		seen := map[string]bool{}
		for _, m := range wiringC4ModeRE.FindAllStringSubmatch(read(rel), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if m[1] != cfg.C4.Mode {
				contradictions = append(contradictions, fmt.Sprintf("%s claims c4.mode: %s but config.yml says %s",
					rel, m[1], cfg.C4.Mode))
			}
		}
	}
	skillDetail := "no skill file contradicts config.yml's c4.mode"
	if len(contradictions) > 0 {
		skillDetail = strings.Join(contradictions, "; ") + " — stale docs steer agents wrong"
	}

	// All three are Advisory by construction: wiring drift informs the human,
	// never flips the pre-flight exit code (ADR-0026 rejected hard-failing).
	return []Check{
		{Name: "CI fail-on matches config", OK: len(weak) == 0, Advisory: true, Detail: failOnDetail},
		{Name: "install pins agree", OK: len(refs) <= 1, Advisory: true, Detail: pinDetail},
		{Name: "skill docs match config", OK: len(contradictions) == 0, Advisory: true, Detail: skillDetail},
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
