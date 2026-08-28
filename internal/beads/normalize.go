package beads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// ReadDir loads the plan store from a working tree rather than from a ref. Every
// other read in nugit goes through git on purpose; this one does not, because
// normalize is a WRITER — it exists to rewrite what `bd export` just put on
// disk, before it is committed.
//
// Reading the working tree does NOT mean reading everything in it: a gitignored
// file is not part of the store, and skipping it is what keeps this consistent
// with the ref-addressed reader, which only ever sees tracked files. `bd` keeps
// its own sidecar logs (interactions.jsonl, events.jsonl) right next to the
// store and repos gitignore them for exactly this reason — without this, one
// such file makes the store look sharded, and every bead in it is then assigned
// to a "plan" named after whichever file it happened to be in.
func ReadDir(root string) (Store, error) {
	dir := filepath.Join(root, ".beads")
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree contributes nothing
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			rel, e := filepath.Rel(root, p)
			if e == nil {
				files = append(files, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		return Store{}, err
	}
	files = keepTracked(root, files)
	if len(files) == 0 {
		return Store{}, fmt.Errorf("no .jsonl store under %s", dir)
	}
	sort.Strings(files)
	st := Store{Files: files, Sharded: len(files) > 1, Stats: map[string]FileStat{}}
	for _, f := range files {
		src, e := os.ReadFile(filepath.Join(root, f))
		if e != nil {
			continue
		}
		issues := ParseJSONL(src)
		for i := range issues {
			issues[i].File = f
		}
		lines := countLines(src)
		st.Stats[f] = FileStat{Lines: lines, Parsed: len(issues), Skipped: lines - len(issues)}
		st.Issues = append(st.Issues, issues...)
	}
	st.DuplicateIDs = duplicateIDs(st.Issues)
	st.Issues = dedupByID(st.Issues)
	assignPlans(&st)
	return st, nil
}

// Normalize returns the canonical bytes of the store, keyed by path.
//
// This is the whole answer to the merge-conflict problem, and it is entirely
// mechanical. `bd export` serializes the whole database in the database's own
// order, so closing one step can move unrelated lines — and two agents who
// touched two different steps still produce two full-file rewrites that git
// cannot reconcile. Canonicalizing gives every bead ONE stable line: two agents
// editing two different beads then produce disjoint hunks, which git merges by
// itself, and the only remaining conflict is the honest one where both edited
// the same step.
//
// Nothing is dropped: each line round-trips through map[string]any with
// json.Number, so fields nugit does not read survive byte-exact in value and
// come back in sorted key order.
//
// With split, each plan is written to its own file under .beads/plans/. That is
// the stronger form of the same idea — an agent working one plan then never
// writes a file another agent's PR also touches — and it makes the file the
// plan, so the grouping no longer depends on how ids were named.
func Normalize(st Store, split bool) (map[string][]byte, error) {
	groups := map[string][]Issue{}
	for _, it := range st.Issues {
		if it.raw == nil {
			continue
		}
		dest := it.File
		if split {
			dest = ".beads/plans/" + planSlug(it.Plan) + ".jsonl"
		}
		groups[dest] = append(groups[dest], it)
	}
	out := map[string][]byte{}
	for dest, issues := range groups {
		sort.SliceStable(issues, func(i, j int) bool {
			if issues[i].Plan != issues[j].Plan {
				return issues[i].Plan < issues[j].Plan
			}
			return naturalLess(issues[i].ID, issues[j].ID)
		})
		var b bytes.Buffer
		for _, it := range issues {
			line, err := json.Marshal(it.raw) // map keys marshal in sorted order
			if err != nil {
				return nil, fmt.Errorf("%s: re-encoding %s: %w", it.File, it.ID, err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		out[dest] = b.Bytes()
	}
	return out, nil
}

// planSlug makes a plan name safe as a file name without inventing collisions:
// only characters that cannot appear in a path are replaced.
func planSlug(name string) string {
	if name == "" {
		return "unplanned"
	}
	repl := func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}
	return strings.Map(repl, name)
}

// naturalLess orders ids the way a person reads them: acme-142-2 before
// acme-142-10, not after. Plain string order would interleave a plan's steps
// with every rewrite, which is precisely the churn this is here to remove.
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ad, bd := isDigit(a[ai]), isDigit(b[bi])
		if ad != bd {
			return a[ai] < b[bi]
		}
		if !ad {
			if a[ai] != b[bi] {
				return a[ai] < b[bi]
			}
			ai, bi = ai+1, bi+1
			continue
		}
		as, bs := ai, bi
		for ai < len(a) && isDigit(a[ai]) {
			ai++
		}
		for bi < len(b) && isDigit(b[bi]) {
			bi++
		}
		an := strings.TrimLeft(a[as:ai], "0")
		bn := strings.TrimLeft(b[bs:bi], "0")
		if len(an) != len(bn) {
			return len(an) < len(bn)
		}
		if an != bn {
			return an < bn
		}
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// keepTracked drops the paths git is told to ignore. A directory that is not a
// git repository (or a git that cannot be run) keeps every file: this is a
// filter on a working tree, and failing it closed would make `plan normalize`
// refuse to work outside a repo for no gain.
func keepTracked(root string, files []string) []string {
	if len(files) == 0 {
		return files
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(files, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		// Exit 1 means "nothing matched" and is the common, healthy case; any
		// other failure (not a repo, no git) leaves the list untouched.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return files
		}
	}
	ignored := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ignored[filepath.ToSlash(line)] = true
		}
	}
	if len(ignored) == 0 {
		return files
	}
	kept := files[:0:0]
	for _, f := range files {
		if !ignored[f] {
			kept = append(kept, f)
		}
	}
	return kept
}
