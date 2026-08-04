package knowledge

// Tests for the ADR-0022 lifecycle-integrity cores: prose-supersession
// detection and generic front-matter inspection.

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestProseSupersessionTargets(t *testing.T) {
	body := "# ADR-20\n\nSupersedes REF-T-14 — that reference is now stale.\n" +
		"It also supersedes ADR-0001 in passing.\n" +
		"But quoting `supersedes: ADR-0002` is documentation, not a declaration.\n" +
		"```\nsupersedes: ADR-0003\n```\n" +
		"Supersedes REF-T-14 again (dedup).\n"
	got := ProseSupersessionTargets(body)
	want := []string{"REF-T-14", "ADR-0001"}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %v, want %v", got, want)
		}
	}
}

func TestProseSupersessionTargetsPlaceholderIgnored(t *testing.T) {
	// The ADR-0003 idiom "supersedes: <old-key>" (a placeholder, not an id)
	// and bare non-dashed words must not match.
	if got := ProseSupersessionTargets("names supersedes: <old-key> at read time\nsupersedes everything\n"); len(got) != 0 {
		t.Fatalf("placeholder matched: %v", got)
	}
}

func obj(id string, typ model.Kind, status model.Status, body string) model.KnowledgeObject {
	return model.KnowledgeObject{
		FrontMatter: model.FrontMatter{ID: id, Type: typ, Status: status},
		Path:        ".nugit/objects/" + id + ".md",
		Body:        body,
	}
}

func TestProseOnlySupersessions(t *testing.T) {
	objs := []model.KnowledgeObject{
		obj("REF-T-14", model.KindReference, model.StatusActive, "# draft 14\n"),
		obj("ADR-20", model.KindDecision, model.StatusAccepted,
			"Supersedes REF-T-14 — now stale.\n"),
	}
	ResolveEffectiveStatus(objs)
	got := ProseOnlySupersessions(objs)
	if len(got) != 1 {
		t.Fatalf("want 1 prose-only supersession, got %v", got)
	}
	p := got[0]
	if p.ObjectID != "ADR-20" || p.Target != "REF-T-14" {
		t.Errorf("wrong pair: %+v", p)
	}
	if p.ObjectPath == "" || p.TargetPath == "" {
		t.Errorf("paths must be carried: %+v", p)
	}
}

func TestProseOnlySupersessionsSilentCases(t *testing.T) {
	cases := []struct {
		name string
		objs []model.KnowledgeObject
	}{
		{
			// The front-matter edge exists: prose and graph agree.
			name: "edge-present",
			objs: []model.KnowledgeObject{
				obj("REF-T-14", model.KindReference, model.StatusActive, ""),
				{FrontMatter: model.FrontMatter{ID: "ADR-20", Type: model.KindDecision,
					Status: model.StatusAccepted, Supersedes: "REF-T-14"},
					Body: "Supersedes REF-T-14 — now stale.\n"},
			},
		},
		{
			// Partial supersession via amends: (ADR-0015) also counts as declared.
			name: "amends-edge-present",
			objs: []model.KnowledgeObject{
				obj("REF-T-14", model.KindReference, model.StatusActive, ""),
				{FrontMatter: model.FrontMatter{ID: "ADR-20", Type: model.KindDecision,
					Status: model.StatusAccepted, RelatesTo: []string{"amends:REF-T-14"}},
					Body: "Supersedes REF-T-14 §3 only.\n"},
			},
		},
		{
			// The named id resolves to nothing in this store: not our drift.
			name: "unresolvable-target",
			objs: []model.KnowledgeObject{
				obj("ADR-20", model.KindDecision, model.StatusAccepted,
					"Supersedes REF-ELSEWHERE-1 upstream.\n"),
			},
		},
		{
			// The target is already superseded by a third object's edge: the
			// drift is resolved, EffectiveStatus already updates.
			name: "target-already-superseded",
			objs: []model.KnowledgeObject{
				obj("REF-T-14", model.KindReference, model.StatusActive, ""),
				{FrontMatter: model.FrontMatter{ID: "ADR-19", Type: model.KindDecision,
					Status: model.StatusAccepted, Supersedes: "REF-T-14"}},
				obj("ADR-20", model.KindDecision, model.StatusAccepted,
					"Supersedes REF-T-14 — now stale.\n"),
			},
		},
		{
			// A dead declarer's prose declares nothing.
			name: "dead-declarer",
			objs: []model.KnowledgeObject{
				obj("REF-T-14", model.KindReference, model.StatusActive, ""),
				obj("ADR-20", model.KindDecision, model.StatusAccepted,
					"Supersedes REF-T-14 — now stale.\n"),
				{FrontMatter: model.FrontMatter{ID: "ADR-21", Type: model.KindDecision,
					Status: model.StatusAccepted, Supersedes: "ADR-20"}},
			},
		},
		{
			// Untyped objects (no type) are invisible to this check too.
			name: "untyped-declarer",
			objs: []model.KnowledgeObject{
				obj("REF-T-14", model.KindReference, model.StatusActive, ""),
				{FrontMatter: model.FrontMatter{ID: "ADR-20"},
					Body: "Supersedes REF-T-14 — now stale.\n"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ResolveEffectiveStatus(tc.objs)
			if got := ProseOnlySupersessions(tc.objs); len(got) != 0 {
				t.Fatalf("must stay silent, got %v", got)
			}
		})
	}
}

func TestRawFrontMatter(t *testing.T) {
	raw, ok := RawFrontMatter("---\nid: ADR-2\nsupersedes:\n  - ADR-1\nprovenance:\n  commit: HEAD\n  issues:\n    - 42\n---\n\n# body\n")
	if !ok {
		t.Fatal("expected ok")
	}
	if _, isList := raw["supersedes"].([]any); !isList {
		t.Errorf("supersedes should surface as a list, got %T", raw["supersedes"])
	}
	pm, isMap := raw["provenance"].(map[string]any)
	if !isMap {
		t.Fatalf("provenance should be a mapping, got %T", raw["provenance"])
	}
	if pm["commit"] != "HEAD" {
		t.Errorf("commit = %v", pm["commit"])
	}
	if _, ok := pm["issues"]; !ok {
		t.Error("unknown provenance keys must survive the raw parse")
	}
	if _, ok := RawFrontMatter("# no front matter\n"); ok {
		t.Error("no block: ok must be false")
	}
}
