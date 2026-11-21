package knowledge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// nugit is built against other repos' stores, and what it learns there is
// written up here as evidence. That evidence belongs to those repos. A store
// key with a namespace prefix (ADR-<PREFIX>-NNNN, SPEC-<PREFIX>-…) or a
// lowercase bead id (<prefix>-<n>) copied from one of them names that repo in
// public. So nugit's own tree carries only the synthetic prefixes below, and
// anything else is reworded: "a pilot ADR", or an acme-style id of the same
// shape.

// fixtureKeyPrefixes are the namespace prefixes of synthetic store keys
// (ADR-ACME-0091, REF-T-14) used in nugit's tests, examples and records.
var fixtureKeyPrefixes = set("ACME", "AUTH", "ELSEWHERE", "P", "PAY", "T")

// fixtureBeadPrefixes are the first segments of synthetic bead ids and of the
// other id-shaped fixtures that sit where a bead id would (session ids,
// tier names).
var fixtureBeadPrefixes = set(
	"acme", "proj", "demo", "api", "adr", "gh", "gr", "bd", "objective", "tile",
	"a", "ab", "f", "p", "r", "x", "xx", "y",
	"deck", "sess", "high",
	"meadow", // proj-meadow-7's short form, as a branch names it
)

var (
	// prefixedKey is a store key whose second segment is a namespace, not a
	// number: ADR-ACME-0091, not ADR-0091.
	prefixedKey = regexp.MustCompile(`\b(?:ADR|SPEC|REF|LESSON)-([A-Z][A-Z0-9]*)-[0-9]`)

	beadID = `([a-z][a-z0-9]*)(?:-[a-z][a-z0-9]*)*-[0-9]+[a-z0-9]*(?:-[a-z0-9]+)*`

	// beadSites are the places a bead id is written: a JSON or struct id, an
	// objective: trailer, a branch name, a code span.
	beadSites = []*regexp.Regexp{
		regexp.MustCompile(`"id\\?"\s*:\s*\\?"` + beadID + `\\?"`),
		regexp.MustCompile(`\bID:\s*"` + beadID + `"`),
		regexp.MustCompile(`\bobjective:\s*` + beadID + `\b`),
		regexp.MustCompile("`" + beadID + "`"),
		regexp.MustCompile(`\b(?:feat|fix|chore|docs)/` + beadID + `\b`),
	}
	// A test's string literal that is exactly a bead id is one, wherever it
	// sits (a table key, an argument).
	beadLiteral = regexp.MustCompile(`"` + beadID + `"`)

	scanned = set(".go", ".md", ".jsonl", ".json", ".yml", ".yaml", ".dsl")
)

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// foreignKeys returns each id in src whose prefix is not a fixture prefix, as
// "line: token".
func foreignKeys(name string, src []byte) []string {
	var out []string
	add := func(at int, token string) {
		out = append(out, fmt.Sprintf("%d: %s", bytes.Count(src[:at], []byte("\n"))+1, token))
	}
	for _, m := range prefixedKey.FindAllSubmatchIndex(src, -1) {
		if !fixtureKeyPrefixes[string(src[m[2]:m[3]])] {
			add(m[0], string(src[m[0]:m[1]]))
		}
	}
	sites := beadSites
	if strings.HasSuffix(name, "_test.go") {
		sites = append(append([]*regexp.Regexp{}, beadSites...), beadLiteral)
	}
	seen := map[int]bool{}
	for _, re := range sites {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			if !fixtureBeadPrefixes[string(src[m[2]:m[3]])] && !seen[m[2]] {
				seen[m[2]] = true
				add(m[0], string(src[m[0]:m[1]]))
			}
		}
	}
	return out
}

// Every tracked text file: the store, the Go code and its fixtures, the skill
// text and its embedded copy, and the docs.
func TestOwnRepoCarriesNoForeignKeys(t *testing.T) {
	root := "../.."
	ls, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	var files, bad []string
	for _, f := range strings.Split(string(ls), "\x00") {
		if scanned[filepath.Ext(f)] {
			files = append(files, f)
		}
	}
	if len(files) < 100 {
		t.Fatalf("listed %d files under %s; the test is not reading the repo", len(files), root)
	}
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // deleted in the working tree
		}
		for _, hit := range foreignKeys(f, src) {
			bad = append(bad, f+":"+hit)
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("%s", b)
	}
	if len(bad) > 0 {
		t.Logf("%d id(s) with a prefix nugit's fixtures do not use. An id copied from another repo's "+
			"store names that repo: reword it (\"a pilot ADR\") or use an acme-style id of the same shape. "+
			"A new synthetic prefix goes in fixtureKeyPrefixes or fixtureBeadPrefixes.", len(bad))
	}
}

// The matcher itself. The foreign tokens are built here, not written out, so
// this file passes its own scan.
func TestForeignKeysMatcher(t *testing.T) {
	foreignKey := "ADR-" + "ZQX" + "-0051"
	foreignBead := "zqx" + "-412"
	cases := []struct {
		name, src string
		want      int
	}{
		{"notes.md", "see " + foreignKey + " and SPEC-" + "ZQX" + "-7", 2},
		{"notes.md", "ADR-0051, ADR-ACME-0091, REF-T-14, LESSON-some-slug-2", 0},
		{"plan.jsonl", `{"id":"` + foreignBead + `","status":"open"}`, 1},
		{"plan.jsonl", `{"id":"acme-rift-1","status":"open"}`, 0},
		{"x.go", `st := []Issue{{ID: "` + foreignBead + `"}}`, 1},
		{"x.md", "objective: " + foreignBead + "\n", 1},
		{"x.md", "branch feat/" + foreignBead + "-retry", 1},
		{"x.md", "`" + foreignBead + "`", 1},
		{"x.go", `m := map[string]string{"` + foreignBead + `": "y"}`, 0}, // not a test file
		{"x_test.go", `m := map[string]string{"` + foreignBead + `": "y"}`, 1},
		{"x_test.go", `"acme-118", "demo-6"`, 0},
	}
	for _, c := range cases {
		if got := foreignKeys(c.name, []byte(c.src)); len(got) != c.want {
			t.Errorf("%s %q: got %v, want %d hit(s)", c.name, c.src, got, c.want)
		}
	}
}
