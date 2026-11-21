package retrieval

import (
	"math"
	"sort"

	"github.com/n8o/nugit/internal/model"
)

// taskWeights is the task's match against every loaded object: whether each
// object contains a task word, and how specifically (ADR-0055). One pass over
// the store answers both, so the keyword gate, the miss bit in the rank and
// the weight can never disagree about what "matches the task" means.
//
// A task word counts by how rare it is in the store: ln(N/df), where N is the
// number of objects loaded and df the number whose text contains the word. A
// word every record contains ("the", "for", the repo's own name) weighs
// nothing, because it cannot say which record the task is about. A word only
// one record contains weighs the most. An object's weight is the sum over the
// distinct task words it contains.
//
// The weight orders items WITHIN a relevance rank and nothing else. It admits
// nothing, cuts nothing and never lifts an item across a tier, so there is no
// threshold and no constant to tune. It is a pure function of the task and the
// store: the same store gives the same order (ADR-0006).
type taskWeights struct {
	given bool // a task with at least one word was given
	// w holds every object that contains a task word, keyed by its address in
	// the loaded slice. Absent = no match.
	w map[*model.KnowledgeObject]float64
}

// weigh matches the task words kw against every object in objs. Text and
// tokenization are what matching has always used: the object's body and id,
// as whole tokens (ADR-0024).
//
// Objects are keyed by address, so every lookup must pass a pointer into this
// same objs slice: the direct pass's &objs[i], and traversal targets, which
// byKey indexes by the same addresses.
func weigh(objs []model.KnowledgeObject, kw map[string]bool) taskWeights {
	tw := taskWeights{given: len(kw) > 0}
	if !tw.given || len(objs) == 0 {
		return tw
	}
	// Task words in a fixed order, so the float sum is the same every run.
	words := make([]string, 0, len(kw))
	for t := range kw {
		words = append(words, t)
	}
	sort.Strings(words)

	found := make([]map[string]bool, len(objs))
	df := map[string]int{}
	for i := range objs {
		var m map[string]bool
		eachToken(objs[i].Body+" "+objs[i].ID, func(tok string) {
			if kw[tok] && !m[tok] {
				if m == nil {
					m = map[string]bool{}
				}
				m[tok] = true
				df[tok]++
			}
		})
		found[i] = m
	}
	n := float64(len(objs))
	tw.w = map[*model.KnowledgeObject]float64{}
	for i := range objs {
		if len(found[i]) == 0 {
			continue
		}
		s := 0.0
		for _, t := range words {
			if found[i][t] {
				s += math.Log(n / float64(df[t]))
			}
		}
		tw.w[&objs[i]] = s
	}
	return tw
}

// matches reports whether o contains at least one task word.
func (tw taskWeights) matches(o *model.KnowledgeObject) bool {
	_, ok := tw.w[o]
	return ok
}

// misses reports a task that o matches none of: the miss bit in an item's
// relevance rank (ADR-0043). With no task, nothing misses.
func (tw taskWeights) misses(o *model.KnowledgeObject) bool { return tw.given && !tw.matches(o) }

// of is o's weight: zero when o matches no task word or no task was given.
func (tw taskWeights) of(o *model.KnowledgeObject) float64 { return tw.w[o] }
