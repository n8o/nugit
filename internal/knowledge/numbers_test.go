package knowledge

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func numbered(id, path string) model.KnowledgeObject {
	return model.KnowledgeObject{
		FrontMatter: model.FrontMatter{ID: id, Type: model.KindDecision},
		Path:        path,
	}
}

func TestNumberKeyOf(t *testing.T) {
	k, ok := NumberKeyOf("", "ADR-0055", ".nugit/decisions/0055-a-change.md")
	if !ok || k.Number != "0055" || k.Namespace != "ADR-" || k.Dir != ".nugit/decisions" {
		t.Fatalf("got %+v %v", k, ok)
	}
	for _, p := range []string{".nugit/decisions/README.md", ".nugit/decisions/0055.md", ".nugit/decisions/0055a-x.md"} {
		if _, ok := NumberKeyOf("", "ADR-0055", p); ok {
			t.Errorf("%s has no leading number and dash, so it has no number key", p)
		}
	}
}

// A renumber done halfway: the id moved to ADR-0063, the file name stayed at
// 0055. The ids differ, so DuplicateIDs is silent, and the number is the only
// place the collision shows.
func TestDuplicateNumbersFindsAHalfDoneRenumber(t *testing.T) {
	objs := []model.KnowledgeObject{
		numbered("ADR-0055", ".nugit/decisions/0055-a-change.md"),
		numbered("ADR-0063", ".nugit/decisions/0055-task-words.md"),
		numbered("ADR-0056", ".nugit/decisions/0056-verified.md"),
	}
	if d := DuplicateIDs(objs); len(d) != 0 {
		t.Fatalf("no id is shared: %+v", d)
	}
	ds := DuplicateNumbers(objs)
	if len(ds) != 1 {
		t.Fatalf("want one shared number, got %+v", ds)
	}
	d := ds[0]
	if d.Number != "0055" || len(d.Paths) != 2 || strings.Join(d.IDs, ",") != "ADR-0055,ADR-0063" {
		t.Errorf("got %+v", d)
	}
	if s := d.Describe(); !strings.Contains(s, "0055-task-words.md") || !strings.Contains(s, "ADR-0063") {
		t.Errorf("the description must name the files and ids: %s", s)
	}
}

func TestDuplicateNumbersLeavesOtherCasesAlone(t *testing.T) {
	cases := map[string][]model.KnowledgeObject{
		// One id twice is DuplicateIDs' finding, not a second one here.
		"shared id": {
			numbered("ADR-0055", ".nugit/decisions/0055-a.md"),
			numbered("ADR-0055", ".nugit/decisions/0055-b.md"),
		},
		// Two namespaces numbered side by side in one directory.
		"namespaces": {
			numbered("ADR-AUTH-0001", ".nugit/decisions/0001-login.md"),
			numbered("ADR-PAY-0001", ".nugit/decisions/0001-refunds.md"),
		},
		// Two stores each have their own 0001 (ADR-0032).
		"stores": {
			numbered("ADR-0001", ".nugit/decisions/0001-a.md"),
			func() model.KnowledgeObject {
				o := numbered("ADR-0009", ".nugit/decisions/0001-b.md")
				o.Origin = "peer"
				return o
			}(),
		},
		// Lessons are not numbered.
		"lessons": {
			numbered("ADR-0001", ".nugit/decisions/0001-a.md"),
			{FrontMatter: model.FrontMatter{ID: "LESSON-x", Type: model.KindLesson}, Path: ".nugit/decisions/0001-x.md"},
		},
	}
	for name, objs := range cases {
		if d := DuplicateNumbers(objs); len(d) != 0 {
			t.Errorf("%s: want no shared number, got %+v", name, d)
		}
	}
}
