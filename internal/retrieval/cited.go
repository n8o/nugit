package retrieval

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// maxCitedScanBytes bounds how much of the queried file is read for citations
// (ADR-0053). A source file that cites a decision does so in its first
// megabyte; past that it is generated or data, and the scan must never make a
// bundle's cost proportional to file size.
const maxCitedScanBytes = 1 << 20

// idToken matches an identifier-shaped run: alphanumerics joined by single
// hyphens. Every store key is one (ADR-0001, ADR-JBS-0091, SPEC-001,
// LESSON-some-slug), and trailing punctuation ("ADR-0043's", "(ADR-0043).")
// falls outside it.
var idToken = regexp.MustCompile(`[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*`)

// citedIDs returns the ids in known that the file at rel names in its own text
// (ADR-0053): a citation the code's author wrote beside the code it governs.
// Exact, case-sensitive key matches only; a token also matches at each of its
// hyphen boundaries, so "ADR-0043-style" still cites ADR-0043. A directory, a
// missing file, a path outside the repo, or an empty known set yields nil:
// citations are an extra signal, never a reason for context() to fail.
func citedIDs(repoDir, rel string, known map[string]bool) map[string]bool {
	if len(known) == 0 {
		return nil
	}
	text := readHead(repoDir, rel, maxCitedScanBytes)
	if text == "" {
		return nil
	}
	var out map[string]bool
	for _, tok := range idToken.FindAllString(text, -1) {
		if !strings.Contains(tok, "-") {
			continue // every key has a hyphen; skip the common case cheaply
		}
		for end := len(tok); end > 0; {
			if p := tok[:end]; known[p] {
				if out == nil {
					out = map[string]bool{}
				}
				out[p] = true
			}
			end = strings.LastIndexByte(tok[:end], '-')
		}
	}
	return out
}

// citedDead lists the cited LOCAL records that are superseded or invalidated,
// sorted by id, as "ID (superseded by X)" / "ID (invalidated)" (ADR-0053).
func citedDead(objs []model.KnowledgeObject, cited map[string]bool) []string {
	if len(cited) == 0 {
		return nil
	}
	by := map[string][]string{} // superseded id -> local ids superseding it
	for _, o := range objs {
		if !o.Foreign() && o.Supersedes != "" && !dead(&o) {
			by[o.Supersedes] = append(by[o.Supersedes], o.ID)
		}
	}
	var out []string
	for i := range objs {
		o := &objs[i]
		if o.Foreign() || !cited[o.ID] || !dead(o) {
			continue
		}
		s := o.ID + " (" + string(effectiveStatus(o))
		if succ := by[o.ID]; len(succ) > 0 {
			sort.Strings(succ)
			s += " by " + strings.Join(succ, ", ")
		}
		out = append(out, s+")")
	}
	sort.Strings(out)
	return out
}

// readHead reads at most n bytes of a regular file inside repoDir. A path
// that is absolute or climbs out of the repo reads nothing: the queried path is
// agent-supplied, and only this repo's files can cite this repo's store.
func readHead(repoDir, rel string, n int64) string {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	p := filepath.Join(repoDir, clean)
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return ""
	}
	return string(b)
}
