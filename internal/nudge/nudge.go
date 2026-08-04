// Package nudge implements the significance-aware capture prompt for the
// commit-msg hook (ADR-0023). The pilot repo showed capture QUALITY is high but
// capture RATE is ~9% on significant commits — the bottleneck is prompting,
// not skill. This package decides, at the only moment the author still holds
// the why (commit time), whether to print a copy-pasteable trailer stub.
//
// Discipline: the nudge never blocks and never errors — every internal
// failure (unborn HEAD, not a git repo, unparsable model, git timeout, even a
// panic) degrades to silence — and it stays fast: one time-bounded git call,
// with a hard staged-file cap beyond which the nudge is skipped rather than
// slowing the commit.
package nudge

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/significance"
	"github.com/n8o/nugit/internal/trailers"
)

const (
	// gitTimeout bounds the one extra git call so the hook stays well inside
	// the ~150ms commit-latency budget even on pathological repos.
	gitTimeout = 100 * time.Millisecond
	// maxStagedFiles: beyond this the nudge is skipped entirely — a diff this
	// large is obviously significant, but classifying and keyword-seeding it
	// is not worth commit latency; pr-render still sees the change.
	maxStagedFiles = 400
	// maxKeywords caps the seeded keywords: a suggestion, not an inventory.
	maxKeywords = 6
)

// ForStagedCommit returns the nudge text for the commit message msg (subject +
// body, as read from the commit-msg hook's message file), or "" when no nudge
// should fire: mode is not nudge, a trailer block is already present, a
// merge/fixup subject, a trivial staged diff, or ANY internal failure (the
// nudge must never block and never slow a commit).
func ForStagedCommit(dir, msg string, cfg config.Config) (out string) {
	defer func() {
		if recover() != nil {
			out = "" // a capture nudge must never break a commit
		}
	}()
	if cfg.Capture.CommitMsg != "nudge" {
		return ""
	}
	subject, body := splitMsg(msg)
	if skipSubject(subject) {
		return ""
	}
	if trailers.Parse(body).Has() {
		return "" // already captured — validation, not nudging, handles hygiene
	}
	repo := gitutil.Repo{Dir: dir}
	counts, err := repo.NumstatCached(gitTimeout)
	if err != nil || len(counts) == 0 || len(counts) > maxStagedFiles {
		return ""
	}
	code := stagedDelta(counts, repo.Prefix(), loadMapper(dir, cfg))
	if len(code.Files) == 0 {
		return ""
	}
	sig := significance.Classify(model.C4Delta{}, code, model.KnowledgeDelta{}, false, significance.Options{
		TrivialMaxFiles: cfg.Significance.TrivialMaxFiles,
		TrivialMaxChurn: cfg.Significance.TrivialMaxChurn,
	})
	if sig.Tier == model.TierTrivial {
		return ""
	}
	return render(sig, keywords(code))
}

// splitMsg splits a commit message into subject (first line) and body.
func splitMsg(msg string) (subject, body string) {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i], msg[i+1:]
	}
	return msg, ""
}

// skipSubject reports whether the commit is machinery (merge, autosquash) that
// should never be nudged — its why lives elsewhere.
func skipSubject(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "Merge ") ||
		strings.HasPrefix(s, "fixup!") ||
		strings.HasPrefix(s, "squash!") ||
		strings.HasPrefix(s, "amend!")
}

// loadMapper parses the working-tree model (a hook has no reviewed ref yet).
// Any failure yields an empty mapper — keywords then fall back to path names.
func loadMapper(dir string, cfg config.Config) *mapping.Mapper {
	dsl := cfg.C4.DSL
	if dsl == "" {
		dsl = ".nugit/architecture/workspace.dsl"
	}
	src, err := os.ReadFile(filepath.Join(dir, dsl))
	if err != nil {
		return mapping.New(model.Model{})
	}
	return mapping.New(c4.Parse(string(src)))
}

// stagedDelta assembles a CodeDelta from the staged numstat. `.nugit/**` is
// excluded (mirroring delta.Code): a commit that only touches the knowledge
// store IS capture and must never be nagged about capture.
func stagedDelta(counts map[string][2]int, prefix string, mp *mapping.Mapper) model.CodeDelta {
	paths := make([]string, 0, len(counts))
	for p := range counts {
		paths = append(paths, p)
	}
	sort.Strings(paths) // deterministic file (and thus keyword) order
	nugitDir := prefix + ".nugit/"
	var d model.CodeDelta
	for _, p := range paths {
		if strings.HasPrefix(p, nugitDir) {
			continue
		}
		c := counts[p]
		d.Files = append(d.Files, model.FileChange{
			Path: p, Added: c[0], Deleted: c[1], Status: "M", Component: mp.Resolve(p),
		})
		d.TotalAdd += c[0]
		d.TotalDel += c[1]
	}
	return d
}

// keywords seeds the stub's keywords: mapped C4 component ids first (most
// touched files wins), then path-derived names for unmapped files.
func keywords(code model.CodeDelta) []string {
	comps := map[string]int{}
	dirs := map[string]int{}
	for _, f := range code.Files {
		if f.Component != "" {
			comps[f.Component]++
		} else if k := pathKeyword(f.Path); k != "" {
			dirs[k]++
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, maxKeywords)
	for _, k := range append(topN(comps, maxKeywords), topN(dirs, maxKeywords)...) {
		lk := strings.ToLower(k)
		if !seen[lk] && len(out) < maxKeywords {
			seen[lk] = true
			out = append(out, k)
		}
	}
	return out
}

// topN returns the n highest-frequency keys, ties broken alphabetically.
func topN(freq map[string]int, n int) []string {
	keys := make([]string, 0, len(freq))
	for k := range freq {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if freq[keys[i]] != freq[keys[j]] {
			return freq[keys[i]] > freq[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// genericDirs are path segments too generic to be useful search keywords.
var genericDirs = map[string]bool{
	"internal": true, "src": true, "pkg": true, "cmd": true, "lib": true,
	"app": true, "apps": true, "test": true, "tests": true, "docs": true,
}

// pathKeyword derives a keyword from an unmapped path: the innermost
// non-generic directory name, else the file stem.
func pathKeyword(p string) string {
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		if b := path.Base(d); !genericDirs[b] {
			return b
		}
	}
	base := path.Base(p)
	return strings.TrimSuffix(base, path.Ext(base))
}

// render formats the nudge: the classifier's reasons (auditable, §13.6) plus a
// copy-pasteable stub. Keep it short — this prints on every significant
// uncaptured commit and must earn its place on the terminal.
func render(sig model.Significance, kws []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nugit: significant commit (%s) with no capture trailer.\n", strings.Join(sig.Reasons, "; "))
	b.WriteString("nugit: future-you will ask why — paste into the commit message and fill in:\n\n")
	b.WriteString("    learned: <the one thing to know before touching this again>\n")
	if len(kws) > 0 {
		fmt.Fprintf(&b, "    keywords: %s\n", strings.Join(kws, ", "))
	} else {
		b.WriteString("    keywords: <search terms for this change>\n")
	}
	b.WriteString("\nnugit: add decision:/rejected: lines when you weighed alternatives.\n")
	b.WriteString("nugit: advisory only — the commit proceeds either way (capture.commit_msg: nudge).\n")
	return b.String()
}
