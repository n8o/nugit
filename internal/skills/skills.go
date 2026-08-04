// Package skills distributes the agent skill files that teach a coding agent to
// USE nugit — the org-wide distribution channel for agent instructions
// (ADR-0035 point 4).
//
// `nugit agent -install` already writes the MCP wiring, so an adopting repo's
// client learns the `context` tool exists. It does not learn WHEN to call it,
// what a trailer block is for, or that a declared architecture edge is not a
// suggestion — all of which lives in the skill files. Until now those were
// hand-copied from this repo into every adopting one, which is a distribution
// channel with no version, no update path, and no way to tell a stale copy from
// a current one. Embedding them in the binary makes the version you installed
// the version you get.
//
// The embedded tree is CANONICAL. This repo's own `.claude/skills/**` is the
// installed artifact — nugit dogfooding its own installer — and a test pins the
// two byte-identical so the duplication cannot rot into two writers for one fact
// (ADR-0011).
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed data
var data embed.FS

// Skill is one distributable skill: its directory name, the repo-relative path
// it installs to, and its content.
type Skill struct {
	// Name is the skill's directory name (`nugit`, `nugit-model`), which is also
	// how a client refers to it.
	Name string
	// Path is where it installs, repo-relative and slash-separated.
	Path string
	// Content is the file's bytes as shipped in the binary.
	Content string
}

// All returns every embedded skill, sorted by name.
func All() []Skill {
	var out []Skill
	_ = fs.WalkDir(data, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		b, rerr := data.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		name := filepath.Base(filepath.Dir(p))
		out = append(out, Skill{
			Name:    name,
			Path:    ".claude/skills/" + name + "/" + filepath.Base(p),
			Content: string(b),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Get returns one skill by name.
func Get(name string) (Skill, bool) {
	for _, s := range All() {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Names lists the installable skill names, for help text and error messages.
func Names() []string {
	var out []string
	for _, s := range All() {
		out = append(out, s.Name)
	}
	return out
}

// Outcome is what happened to one skill file during an install.
type Outcome struct {
	Skill
	// Created is true when the file was written.
	Created bool
	// Unchanged is true when the file already existed with identical bytes —
	// distinct from Skipped, because "your copy is current" and "your copy
	// differs and I left it alone" are different answers.
	Unchanged bool
	// Skipped is true when a DIFFERENT file already existed and force was unset.
	Skipped bool
}

// Install writes the embedded skills into repoDir, mirroring
// `agentcfg.InstallClaudeCode`'s contract: an existing file is never
// overwritten without -force, and it is never merged — a repo's SKILL.md may
// carry local edits, and a deterministic tool does not rewrite prose it did not
// author. An identical existing file is reported as unchanged, not skipped, so
// re-running the installer is quiet and idempotent.
//
// `only` restricts the install to one skill by name; empty installs all.
func Install(repoDir string, force bool, only string) ([]Outcome, error) {
	list := All()
	if only != "" {
		s, ok := Get(only)
		if !ok {
			return nil, fmt.Errorf("unknown skill %q (have: %s)", only, strings.Join(Names(), ", "))
		}
		list = []Skill{s}
	}
	var out []Outcome
	for _, s := range list {
		o := Outcome{Skill: s}
		abs := filepath.Join(repoDir, filepath.FromSlash(s.Path))
		if b, err := os.ReadFile(abs); err == nil {
			switch {
			case string(b) == s.Content:
				o.Unchanged = true
				out = append(out, o)
				continue
			case !force:
				o.Skipped = true
				out = append(out, o)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return out, err
		}
		if err := os.WriteFile(abs, []byte(s.Content), 0o644); err != nil {
			return out, fmt.Errorf("writing %s: %w", s.Path, err)
		}
		o.Created = true
		out = append(out, o)
	}
	return out, nil
}
