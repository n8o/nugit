package promote

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// rewrite produces the bytes that land in the hub. It is a LINE-LEVEL edit of
// the front-matter fence, not a YAML re-marshal, for the reason ADR-0016 already
// recorded when it rejected re-marshalling in `ratify`: round-tripping through a
// YAML encoder rewrites key order, quoting and comments across the whole header,
// so the hub owner's review diff would be "the whole file" instead of "these
// three lines differ from the source". Everything outside the two edits — the
// body, `relates_to`, `applies_to_paths`, `parties`, `created`, the id — is
// byte-identical to the origin.
//
// Two edits, both required:
//
//  1. `status:` becomes `proposed`. The hub owner ratifies (ADR-0016); arriving
//     pre-ratified would let any repo write into another repo's corpus.
//  2. The `provenance:` block is REPLACED by one naming the origin repo, the
//     origin path and the source commit. Replaced rather than merged: the
//     origin's provenance describes how the record came to exist THERE, and
//     keeping it alongside would leave the hub with two answers to "where did
//     this come from". The origin's own citation is preserved as a note under
//     the new block's citation so nothing is destroyed.
func rewrite(content, originRepo, originPath, commit string) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("no front-matter fence")
	}
	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return "", fmt.Errorf("unterminated front-matter fence")
	}
	head, tail := content[:3+end], content[3+end:]

	lines := strings.Split(head, "\n")
	out := make([]string, 0, len(lines)+6)
	sawStatus, inProv := false, false
	var oldCitation string
	for _, ln := range lines {
		if inProv {
			// The provenance mapping runs until the next top-level key (or a
			// non-indented line); its own entries are indented.
			if isIndented(ln) || strings.TrimSpace(ln) == "" {
				if c := provCitation(ln); c != "" {
					oldCitation = c
				}
				continue
			}
			inProv = false
		}
		switch {
		case statusLineRE.MatchString(ln):
			sawStatus = true
			out = append(out, statusLineRE.ReplaceAllString(ln, "status: "+string(model.StatusProposed)+"$1"))
		case provKeyRE.MatchString(ln):
			inProv = true
		default:
			out = append(out, ln)
		}
	}
	if !sawStatus {
		return "", fmt.Errorf("no `status:` line in front-matter")
	}
	// Trailing blank lines inside the fence would push the block past them.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	out = append(out, provenanceBlock(originRepo, originPath, commit, oldCitation)...)
	return strings.Join(out, "\n") + tail, nil
}

// statusLineRE matches any authored status line inside the fence (the capture
// preserves a CRLF line ending). Unlike ratify's, it is not pinned to
// `proposed`: promote rewrites whatever the record holds down to the candidate
// lane.
var statusLineRE = regexp.MustCompile(`^status:[ \t]*\S+[ \t]*(\r?)$`)

// provKeyRE matches the `provenance:` key that opens the block being replaced.
var provKeyRE = regexp.MustCompile(`^provenance:[ \t]*(\r?)$`)

// provCitationRE captures an existing `citation:` inside the old provenance
// block, so promotion preserves rather than discards the origin's own note.
var provCitationRE = regexp.MustCompile(`^[ \t]+citation:[ \t]*(.*?)[ \t]*\r?$`)

func provCitation(ln string) string {
	m := provCitationRE.FindStringSubmatch(ln)
	if m == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
}

func isIndented(s string) bool {
	return strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "-")
}

// provenanceBlock is the hub's answer to "where did this come from": a typed
// origin repo + path pair (resolvable mechanically) and the source commit (which
// pins the exact bytes), plus a sentence naming the tool that wrote it.
func provenanceBlock(originRepo, originPath, commit, oldCitation string) []string {
	if commit == "" {
		commit = "unknown"
	}
	citation := fmt.Sprintf("promoted from %s (%s) by nugit promote", originRepo, originPath)
	if oldCitation != "" {
		citation += "; origin cited: " + oldCitation
	}
	return []string{
		"provenance:",
		"  commit: " + commit,
		"  origin_repo: " + originRepo,
		"  origin_path: " + yamlScalar(originPath),
		"  citation: " + yamlScalar(citation),
	}
}

// yamlScalar quotes a value that would otherwise be ambiguous as a bare YAML
// scalar. Deliberately minimal: paths and generated sentences are the only
// inputs, and a double-quoted string with escaped quotes/backslashes is valid
// YAML for every one of them.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, `:#{}[],&*?|<>=!%@\"'`+"\n\t") || strings.HasPrefix(s, " ") {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\t", " ")
		return `"` + r.Replace(s) + `"`
	}
	return s
}
