package render

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestC4SectionContainerBullets(t *testing.T) {
	d := model.C4Delta{
		AddedContainers:   []model.Container{{ID: "ingest", Name: "Ingest"}},
		RemovedContainers: []model.Container{{ID: "legacy"}},
		ChangedContainers: []model.ContainerChange{{
			Before: model.Container{ID: "encoder", Paths: []string{"a/**"}},
			After:  model.Container{ID: "encoder", Paths: []string{"b/**"}},
			Fields: []string{"paths"},
		}},
	}
	got := c4Section(d)
	for _, want := range []string{
		"- ➕ container **ingest** (Ingest)\n",
		"- ➖ container **legacy** (legacy)\n",
		"- ± container **encoder** changed: paths\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("c4Section missing %q in:\n%s", want, got)
		}
	}
}

// A flat delta renders byte-identically: no container bullets, no summary drift.
func TestC4SectionFlatUnchanged(t *testing.T) {
	d := model.C4Delta{AddedComponents: []model.Component{{ID: "a"}}}
	got := c4Section(d)
	if strings.Contains(got, "container") {
		t.Errorf("flat delta grew container output:\n%s", got)
	}
	if got != "- ➕ component **a** (a)\n" {
		t.Errorf("flat c4Section drifted: %q", got)
	}
}

func TestC4SummaryContainers(t *testing.T) {
	d := model.C4Delta{
		AddedContainers:   []model.Container{{ID: "x"}},
		RemovedContainers: []model.Container{{ID: "y"}},
	}
	if got := c4Summary(d); got != "+1 container(s), -1 container(s)" {
		t.Errorf("c4Summary = %q", got)
	}
	only := model.C4Delta{ChangedContainers: []model.ContainerChange{{Fields: []string{"paths"}}}}
	if got := c4Summary(only); got != "container metadata changed" {
		t.Errorf("container-metadata summary = %q", got)
	}
	flat := model.C4Delta{ChangedComponents: []model.ComponentChange{{Fields: []string{"paths"}}}}
	if got := c4Summary(flat); got != "component metadata changed" {
		t.Errorf("flat metadata summary drifted: %q", got)
	}
}
