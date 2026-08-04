package distill

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// The exported dedup surface exists so `nugit promote` reaches the SAME rule
// distill uses (ADR-0035 point 7). These pin that it is the same rule, not a
// parallel one that can drift.

func TestSimilarKeywordsIsTheADR0018Rule(t *testing.T) {
	cases := []struct {
		name                string
		candidate, existing []string
		want                bool
	}{
		{"two shared covering half", []string{"registry", "retention", "artifacts"},
			[]string{"registry", "retention", "cleanup"}, true},
		{"two shared but under half", []string{"a", "b", "c", "d", "e"},
			[]string{"a", "b", "z"}, false},
		{"one shared is never enough", []string{"registry", "retention"},
			[]string{"registry", "unrelated"}, false},
		{"a single-keyword candidate can never match", []string{"registry"},
			[]string{"registry"}, false},
		{"case and whitespace are normalized away", []string{" Registry ", "RETENTION"},
			[]string{"registry", "retention"}, true},
		{"nothing shared", []string{"alpha", "beta"}, []string{"gamma", "delta"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SimilarKeywords(c.candidate, c.existing); got != c.want {
				t.Errorf("SimilarKeywords(%v, %v) = %v, want %v", c.candidate, c.existing, got, c.want)
			}
		})
	}
}

// The exported predicate and the one distill's index uses must be the same code
// path; if they diverge, the same pair of lessons is a duplicate in one place
// and a novelty in the other.
func TestExportedRuleAgreesWithTheIndex(t *testing.T) {
	kws := []string{"registry", "retention", "artifacts"}
	existing := []string{"registry", "retention", "cleanup"}
	ix := indexStore([]model.KnowledgeObject{{
		FrontMatter: model.FrontMatter{ID: "LESSON-x", Type: model.KindLesson},
		Body:        "# Lesson — x\n\n**Insight:** something.\n\n**Keywords:** " + strings.Join(existing, ", ") + "\n",
	}})
	if !ix.dupLesson("no exact match", kws) {
		t.Fatal("the index did not dedup a keyword-overlapping lesson")
	}
	if !SimilarKeywords(kws, existing) {
		t.Fatal("the exported rule disagrees with the index on the same input")
	}
}

func TestTitleWordsDropsStopWords(t *testing.T) {
	got := TitleWords("The registry reaps the artifacts that are not tagged")
	for _, bad := range []string{"the", "that", "are", "not"} {
		for _, g := range got {
			if g == bad {
				t.Errorf("stop word %q survived: %v", bad, got)
			}
		}
	}
	if len(got) < 3 {
		t.Errorf("TitleWords stripped too much: %v", got)
	}
}

func TestKeywordsAndInsightReadTheDistilledShape(t *testing.T) {
	body := "# Lesson — x\n\n**Insight:** the cursor advanced early.\n\n**Keywords:** cursor, batch\n"
	if got := Insight(body); got != "the cursor advanced early." {
		t.Errorf("Insight = %q", got)
	}
	if got := Keywords(body); len(got) != 2 || got[0] != "cursor" {
		t.Errorf("Keywords = %v", got)
	}
	if got := Normalize("  Two   Words \n"); got != "two words" {
		t.Errorf("Normalize = %q", got)
	}
}
