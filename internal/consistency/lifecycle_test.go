package consistency

// Tests for the prose-supersession check (ADR-0022): a PR-touched knowledge
// object declaring a supersession only in prose warns; declared edges, and
// drift the PR didn't touch, stay silent.

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func lifecycleInput(all []model.KnowledgeObject, changes ...model.KnowledgeChange) Input {
	return Input{
		AllObjects: all,
		Knowledge:  model.KnowledgeDelta{Changes: changes},
	}
}

func kobj(id string, typ model.Kind, status model.Status, supersedes, body string) model.KnowledgeObject {
	o := model.KnowledgeObject{
		FrontMatter: model.FrontMatter{ID: id, Type: typ, Status: status, Supersedes: supersedes},
		Path:        ".nugit/x/" + id + ".md",
		Body:        body,
	}
	o.EffectiveStatus = status
	return o
}

func TestProseSupersessionWarns(t *testing.T) {
	ref := kobj("REF-T-14", model.KindReference, model.StatusActive, "", "# draft 14\n")
	adr := kobj("ADR-20", model.KindDecision, model.StatusAccepted, "",
		"Supersedes REF-T-14 — that reference is now stale.\n")
	in := lifecycleInput([]model.KnowledgeObject{ref, adr},
		model.KnowledgeChange{Path: adr.Path, Status: "A", Object: &adr})

	fs := checkProseSupersession(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	f := fs[0]
	if f.Check != "prose-supersession" || f.Severity != model.SevWarn {
		t.Errorf("check/severity = %s/%s, want prose-supersession/warn", f.Check, f.Severity)
	}
	if f.Title != "ADR-20 supersedes REF-T-14 in prose only" {
		t.Errorf("title = %q", f.Title)
	}
	if !strings.Contains(f.Detail, "supersedes: REF-T-14") || !strings.Contains(f.Detail, "amends:REF-T-14") {
		t.Errorf("detail must give both remediations, got %q", f.Detail)
	}
}

func TestProseSupersessionSilentWhenEdgeDeclared(t *testing.T) {
	ref := kobj("REF-T-14", model.KindReference, model.StatusActive, "", "# draft 14\n")
	ref.EffectiveStatus = model.StatusSuperseded // derived from the edge below
	adr := kobj("ADR-20", model.KindDecision, model.StatusAccepted, "REF-T-14",
		"Supersedes REF-T-14 — that reference is now stale.\n")
	in := lifecycleInput([]model.KnowledgeObject{ref, adr},
		model.KnowledgeChange{Path: adr.Path, Status: "A", Object: &adr})

	if fs := checkProseSupersession(in); len(fs) != 0 {
		t.Fatalf("edge declared: must stay silent, got %+v", fs)
	}
}

// Pre-existing store drift the PR didn't touch is doctor's job — pr-render
// must not nag every PR about it.
func TestProseSupersessionScopedToTouchedObjects(t *testing.T) {
	ref := kobj("REF-T-14", model.KindReference, model.StatusActive, "", "# draft 14\n")
	adr := kobj("ADR-20", model.KindDecision, model.StatusAccepted, "",
		"Supersedes REF-T-14 — that reference is now stale.\n")
	other := kobj("LES-1", model.KindLesson, model.StatusActive, "", "# unrelated\n")
	in := lifecycleInput([]model.KnowledgeObject{ref, adr, other},
		model.KnowledgeChange{Path: other.Path, Status: "A", Object: &other})

	if fs := checkProseSupersession(in); len(fs) != 0 {
		t.Fatalf("untouched drift must stay silent in pr-render, got %+v", fs)
	}
	// And with no knowledge changes at all, nothing runs.
	if fs := checkProseSupersession(lifecycleInput([]model.KnowledgeObject{ref, adr})); len(fs) != 0 {
		t.Fatalf("no knowledge delta: must stay silent, got %+v", fs)
	}
}

func TestProseSupersessionDeletedObjectIgnored(t *testing.T) {
	ref := kobj("REF-T-14", model.KindReference, model.StatusActive, "", "# draft 14\n")
	adr := kobj("ADR-20", model.KindDecision, model.StatusAccepted, "",
		"Supersedes REF-T-14 — that reference is now stale.\n")
	in := lifecycleInput([]model.KnowledgeObject{ref, adr},
		model.KnowledgeChange{Path: adr.Path, Status: "D", Object: &adr})

	if fs := checkProseSupersession(in); len(fs) != 0 {
		t.Fatalf("a deleted object is not a declaration, got %+v", fs)
	}
}

func TestProseSupersessionHasExplanation(t *testing.T) {
	if _, ok := Explain("prose-supersession"); !ok {
		t.Fatal("prose-supersession must have an explain entry")
	}
}
