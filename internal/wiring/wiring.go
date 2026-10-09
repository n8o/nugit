// Package wiring scans the artifacts that wire nugit into a repo — CI
// workflows, CLAUDE.md, agent skill files — for drift against the enforcement
// config.yml declares (ADR-0026): a workflow pinned to `-fail-on none` under an
// enforce config, install pins that disagree, skill prose asserting a stale
// c4.mode.
//
// It exists as its own package because the same scan has to run from two
// surfaces that cannot share a working tree: `nugit doctor` reads the checkout,
// and `nugit pr-render` reads the reviewed ref. A second implementation of
// these rules is the drift it is here to catch (ADR-0041).
//
// Everything is a tolerant regex scan, never a YAML parse: an unreadable or
// oddly-formatted file is skipped rather than failing the scan.
package wiring

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/config"
)

var (
	// failOnRE matches both CLI style (`-fail-on none`) and action-input style
	// (`fail-on: none`). config.yml's own `fail_on` (underscore) does not match
	// by construction.
	failOnRE = regexp.MustCompile(`fail-on[:=\s]+["']?(none|warn|fail)\b`)
	// pinRE matches install/uses pins in any spelling: `nugit@main`,
	// `n8o/nugit@v0.3.0`, `github.com/n8o/nugit/cmd/nugit@latest`.
	pinRE = regexp.MustCompile(`\bnugit@([A-Za-z0-9._-]+)`)
	// c4ModeRE matches a config or prose assertion like `c4.mode: warn`.
	c4ModeRE = regexp.MustCompile(`c4\.mode:\s*["']?(warn|enforce)\b`)
)

// Source supplies the files to scan and their contents. Both are injected so
// the caller decides whether "the repo" means a checkout or a git ref.
type Source struct {
	// ClaudeMD, Skills and Workflows are repo-relative paths, sorted. Any may
	// be empty; a repo without that artifact simply contributes no evidence.
	ClaudeMD  []string
	Skills    []string
	Workflows []string
	// Read returns a file's contents, or "" when it cannot be read.
	Read func(rel string) string
}

// Report is the scan result. Detail strings live here, not in the callers, so
// doctor and pr-render cannot describe the same drift two different ways.
type Report struct {
	// WeakFailOn names workflows running a weaker fail-on than config declares.
	WeakFailOn []string
	// SawFailOn is true when any workflow mentioned a fail-on at all.
	SawFailOn bool
	// Pins maps a pinned ref to the files pinning it.
	Pins map[string][]string
	// Refs are the distinct pinned refs, sorted.
	Refs []string
	// C4Contradictions names skill files asserting a c4.mode config denies.
	C4Contradictions []string
}

// Scan runs all three checks over src, comparing against cfg.
func Scan(src Source, cfg config.Config) Report {
	read := src.Read
	if read == nil {
		read = func(string) string { return "" }
	}
	rep := Report{Pins: map[string][]string{}}

	// (a) a workflow running nugit with a weaker fail-on than config declares.
	for _, rel := range src.Workflows {
		seen := map[string]bool{}
		for _, m := range failOnRE.FindAllStringSubmatch(read(rel), -1) {
			rep.SawFailOn = true
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if config.FailOnRank(m[1]) < config.FailOnRank(cfg.PRRender.FailOn) {
				rep.WeakFailOn = append(rep.WeakFailOn,
					fmt.Sprintf("%s runs fail-on %s but config.yml says %s", rel, m[1], cfg.PRRender.FailOn))
			}
		}
	}

	// (b) install pins agree across CLAUDE.md, skill files, and workflows.
	//
	// Every surface that names a version is scanned, because the one that
	// drifts is whichever the last person editing versions forgot — on the
	// pilot that was the skill file, four weeks after the other four were
	// aligned by hand.
	for _, group := range [][]string{src.ClaudeMD, src.Skills, src.Workflows} {
		for _, rel := range group {
			seen := map[string]bool{}
			for _, m := range pinRE.FindAllStringSubmatch(read(rel), -1) {
				if seen[m[1]] {
					continue
				}
				seen[m[1]] = true
				rep.Pins[m[1]] = append(rep.Pins[m[1]], rel)
			}
		}
	}
	for ref := range rep.Pins {
		rep.Refs = append(rep.Refs, ref)
	}
	sort.Strings(rep.Refs)

	// (c) a skill file asserting a c4.mode that contradicts config.yml.
	for _, rel := range src.Skills {
		seen := map[string]bool{}
		for _, m := range c4ModeRE.FindAllStringSubmatch(read(rel), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if m[1] != cfg.C4.Mode {
				rep.C4Contradictions = append(rep.C4Contradictions,
					fmt.Sprintf("%s claims c4.mode: %s but config.yml says %s", rel, m[1], cfg.C4.Mode))
			}
		}
	}
	return rep
}

// PinsAgree reports whether at most one ref is pinned anywhere.
func (r Report) PinsAgree() bool { return len(r.Refs) <= 1 }

// Clean reports whether the scan found no drift at all.
func (r Report) Clean() bool {
	return len(r.WeakFailOn) == 0 && r.PinsAgree() && len(r.C4Contradictions) == 0
}

// FailOnDetail describes (a).
func (r Report) FailOnDetail(cfg config.Config) string {
	switch {
	case len(r.WeakFailOn) > 0:
		return strings.Join(r.WeakFailOn, "; ") + " — enforcement is weaker in CI than the repo declares"
	case !r.SawFailOn:
		return "no fail-on found under .github/workflows"
	default:
		return fmt.Sprintf("no workflow weakens pr_render.fail_on: %s", cfg.PRRender.FailOn)
	}
}

// PinDetail describes (b).
func (r Report) PinDetail() string {
	switch len(r.Refs) {
	case 0:
		return "no nugit@<ref> pins found"
	case 1:
		return fmt.Sprintf("all pins agree on @%s (%s)", r.Refs[0], strings.Join(r.Pins[r.Refs[0]], ", "))
	default:
		var parts []string
		for _, ref := range r.Refs {
			parts = append(parts, fmt.Sprintf("@%s (%s)", ref, strings.Join(r.Pins[ref], ", ")))
		}
		return fmt.Sprintf("%d different nugit pins: %s — align them so every surface runs the same version",
			len(r.Refs), strings.Join(parts, ", "))
	}
}

// C4Detail describes (c).
func (r Report) C4Detail() string {
	if len(r.C4Contradictions) == 0 {
		return "no skill file contradicts config.yml's c4.mode"
	}
	return strings.Join(r.C4Contradictions, "; ") + " — stale docs steer agents wrong"
}

// IsWiringPath reports whether a repo-relative path is one of the artifacts
// this package scans. Callers use it to decide whether a change is even
// capable of causing wiring drift.
func IsWiringPath(rel string) bool {
	switch {
	case rel == "CLAUDE.md", rel == "AGENTS.md":
		return true
	case strings.HasPrefix(rel, ".claude/skills/"):
		return true
	case strings.HasPrefix(rel, ".github/workflows/"):
		return strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")
	}
	return false
}
