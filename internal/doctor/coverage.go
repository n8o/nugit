package doctor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

// coverageWindowDays / coverageMaxCommits bound the churn scan. 90 days is the
// same window the recurrence check uses; the commit cap is what keeps this
// O(window) on a repo with a long history.
const (
	coverageWindowDays = 90
	coverageMaxCommits = 2000
	coverageHotspots   = 5
)

// thinHotspots names the components that changed most in the recent window and
// carry no scoped knowledge.
//
// doctor has always been able to say how many components are uncovered. On the
// pilot that number was 68 of 92 — true, and useless: it names no first move,
// and the honest response to "74% of your model is uncovered" is to ignore it.
// Worse, the same store grew from 87 to 220 objects over six weeks while that
// ratio got WORSE, because capture lands where capture already is.
//
// Churn is the tiebreak that makes the number actionable. A component nobody
// has touched in three months is uncovered at no cost; one that changed twenty
// times is where the next debugging session will start, with nothing waiting
// for it. Ranking by it turns a wall into five names (ADR-0041).
//
// Advisory and purely descriptive: it adds no deduction, because the orphan
// ratio is already scored and double-counting the same gap would move a number
// adopters track over time.
func thinHotspots(repoDir string, m model.Model, objs []model.KnowledgeObject, orphans []string) []string {
	if len(orphans) == 0 || len(m.Components) == 0 {
		return nil
	}
	repo := gitutil.Repo{Dir: repoDir}
	churn, err := repo.ChurnSince("HEAD", coverageWindowDays, coverageMaxCommits)
	if err != nil || len(churn) == 0 {
		return nil
	}
	isOrphan := map[string]bool{}
	for _, id := range orphans {
		isOrphan[id] = true
	}
	mp := mapping.New(m)
	score := map[string]int{}
	for path, n := range churn {
		comp := mp.Resolve(path)
		if comp == "" || !isOrphan[comp] {
			continue
		}
		score[comp] += n
	}
	if len(score) == 0 {
		return nil
	}
	ids := make([]string, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	// Churn desc, then id — a stable order, so two runs over the same history
	// print the same list.
	sort.Slice(ids, func(i, j int) bool {
		if score[ids[i]] != score[ids[j]] {
			return score[ids[i]] > score[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > coverageHotspots {
		ids = ids[:coverageHotspots]
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("%s (%d commit(s))", id, score[id]))
	}
	return out
}

// edgeVocabularyCheck reports `relates_to` entries nugit cannot act on, plus
// the share of edges carrying no verb at all.
//
// The bare share is reported rather than flagged. A bare id is a legitimate
// "see also" and 368 separate findings would be noise — but a store where
// almost every edge is bare has a lifecycle graph that exists only in prose,
// and that is worth one sentence a maintainer can act on.
func edgeVocabularyCheck(objs []model.KnowledgeObject) Check {
	st := knowledge.EdgeAudit(objs)
	if st.Total == 0 {
		return Check{Name: "relates_to verbs are known", OK: true, Advisory: true,
			Detail: "no relates_to edges in the store"}
	}
	var parts []string
	if len(st.Problems) > 0 {
		var bad []string
		for _, p := range st.Problems {
			if p.Misplaced != "" {
				bad = append(bad, fmt.Sprintf("%s in %s (belongs in %s)", p.Raw, p.Path, p.Misplaced))
				continue
			}
			bad = append(bad, fmt.Sprintf("%s in %s", p.Raw, p.Path))
		}
		if len(bad) > 6 {
			bad = append(bad[:6], fmt.Sprintf("… and %d more", len(st.Problems)-6))
		}
		parts = append(parts, fmt.Sprintf("%d unusable edge(s): %s", len(st.Problems), strings.Join(bad, "; ")))
	}
	parts = append(parts, fmt.Sprintf("%d edge(s): %d semantic, %d scope, %d annotation, %d bare (%d%%)",
		st.Total, st.ByKind[knowledge.RelationSemantic], st.ByKind[knowledge.RelationScope],
		st.ByKind[knowledge.RelationAnnotation], st.Bare, st.BarePercent()))
	if st.BarePercent() >= 75 && st.Total >= 20 {
		parts = append(parts, fmt.Sprintf("%d%% of edges carry no verb, so this store's supersession, amendment and reinforcement graph is almost entirely unexpressed — a bare id resolves in retrieval and means nothing more", st.BarePercent()))
	}
	return Check{
		Name: "relates_to verbs are known", OK: len(st.Problems) == 0, Advisory: true,
		Detail: strings.Join(parts, "; "),
	}
}
