package consistency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// driftModel declares one component bound to libs/core/** only.
func driftModel(extra ...model.Component) model.Model {
	m := model.Model{Components: append([]model.Component{
		{ID: "core", Name: "Core", Paths: []string{"libs/core/**"}},
	}, extra...)}
	return m
}

// cmakeRepo: libs/core (modeled) + libs/newlib (detected, unmodeled).
func cmakeRepo(t *testing.T) string {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"CMakeLists.txt":           "project(demo)\nadd_subdirectory(libs/core)\nadd_subdirectory(libs/newlib)\n",
		"libs/core/CMakeLists.txt": "add_library(core core.cpp)\n",
		"libs/core/core.cpp":       "int core(){return 0;}\n",
		// The pilot shape: a lib add_subdirectory()'d from the root — visible
		// to the CMake detector — that never made it into workspace.dsl.
		"libs/newlib/CMakeLists.txt": "add_library(newlib newlib.cpp)\n",
		"libs/newlib/newlib.cpp":     "int newlib(){return 0;}\n",
	})
	return root
}

func driftInput(repoDir string, m model.Model, touched ...model.FileChange) Input {
	return Input{
		RepoDir:   repoDir,
		HeadModel: m,
		Mapper:    mapping.New(m),
		Code:      model.CodeDelta{Files: touched},
	}
}

// A detected CMake unit absent from the DSL, touched by the PR, must warn.
func TestModelDriftCMakeUnitWarns(t *testing.T) {
	root := cmakeRepo(t)
	in := driftInput(root, driftModel(),
		model.FileChange{Path: "libs/newlib/newlib.cpp", Status: "M", Component: ""})
	fs := checkModelDrift(in)
	if len(fs) != 1 {
		t.Fatalf("want exactly one model-drift finding, got %+v", fs)
	}
	f := fs[0]
	if f.Check != "model-drift" || f.Severity != model.SevWarn {
		t.Errorf("want warn-severity model-drift, got %+v", f)
	}
	if !strings.Contains(f.Title, "libs/newlib") {
		t.Errorf("finding must name the unit, got %q", f.Title)
	}
	if !strings.Contains(f.Detail, "nugit-model") || !strings.Contains(f.Detail, `libs/newlib/**`) {
		t.Errorf("detail must offer the skill and a stub glob, got %q", f.Detail)
	}
}

// The same unit is silent once the model maps it (present -> clean).
func TestModelDriftMappedUnitClean(t *testing.T) {
	root := cmakeRepo(t)
	m := driftModel(model.Component{ID: "newlib", Name: "New Lib", Paths: []string{"libs/newlib/**"}})
	in := driftInput(root, m,
		model.FileChange{Path: "libs/newlib/newlib.cpp", Status: "M", Component: "newlib"})
	if fs := checkModelDrift(in); len(fs) != 0 {
		t.Fatalf("mapped unit must be clean, got %+v", fs)
	}
}

// An unmodeled unit the PR does NOT touch stays silent (noise control: the
// full-repo backlog belongs to doctor, not to unrelated PRs).
func TestModelDriftUntouchedUnitSilent(t *testing.T) {
	root := cmakeRepo(t)
	in := driftInput(root, driftModel(),
		model.FileChange{Path: "libs/core/core.cpp", Status: "M", Component: "core"})
	if fs := checkModelDrift(in); len(fs) != 0 {
		t.Fatalf("untouched drift must not warn on this PR, got %+v", fs)
	}
}

// An element whose name matches the unit (but has no paths) is a binding gap,
// not absence — drift stays silent (model-health owns that story).
func TestModelDriftNameMatchedElementSilent(t *testing.T) {
	root := cmakeRepo(t)
	in := driftInput(root, driftModel(model.Component{ID: "newlib", Name: "New Lib"}),
		model.FileChange{Path: "libs/newlib/newlib.cpp", Status: "M", Component: ""})
	if fs := checkModelDrift(in); len(fs) != 0 {
		t.Fatalf("name-matched element must suppress drift, got %+v", fs)
	}
}

// A Go package dir absent from the DSL, touched by the PR, must warn too.
func TestModelDriftGoUnitWarns(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":               "module example.com/m\n\ngo 1.25\n",
		"libs/core/core.go":    "package core\n\nfunc Core() {}\n",
		"internal/newpkg/n.go": "package newpkg\n\nfunc N() {}\n",
	})
	in := driftInput(root, driftModel(),
		model.FileChange{Path: "internal/newpkg/n.go", Status: "A", Component: ""})
	fs := checkModelDrift(in)
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "internal/newpkg") {
		t.Fatalf("want one model-drift finding for internal/newpkg, got %+v", fs)
	}
}

// A structural (init-generated, relationship-less) model is never graded.
func TestModelDriftStructuralModelSilent(t *testing.T) {
	root := cmakeRepo(t)
	m := driftModel()
	m.Properties = map[string]string{"nugit_structural": "true"}
	in := driftInput(root, m,
		model.FileChange{Path: "libs/newlib/newlib.cpp", Status: "M", Component: ""})
	if fs := checkModelDrift(in); len(fs) != 0 {
		t.Fatalf("structural model must not be graded, got %+v", fs)
	}
}

// Prefix bridging: with the nugit root nested at apps/op/, changed paths are
// git-root-relative while detector dirs are nugit-root-relative.
func TestModelDriftNestedPrefix(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":      "module example.com/op\n\ngo 1.25\n",
		"newpkg/n.go": "package newpkg\n\nfunc N() {}\n",
	})
	m := model.Model{Components: []model.Component{
		{ID: "other", Name: "Other", Paths: []string{"apps/op/other/**"}},
	}}
	in := driftInput(root, m,
		model.FileChange{Path: "apps/op/newpkg/n.go", Status: "A", Component: ""})
	in.Prefix = "apps/op/"
	fs := checkModelDrift(in)
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "newpkg") {
		t.Fatalf("want one model-drift finding for the nested unit, got %+v", fs)
	}
}
