package goimports

import "testing"

func TestAnalyzeInternalImports(t *testing.T) {
	mod := "example.com/m"
	dirs, inc := Analyze(mod, "a/a.go", "package a\n\nimport _ \"example.com/m/b\"\n")
	if !inc {
		t.Fatal("normal file should be included")
	}
	if len(dirs) != 1 || dirs[0] != "b" {
		t.Errorf("dirs = %v, want [b]", dirs)
	}
}

func TestAnalyzeExclusions(t *testing.T) {
	mod := "example.com/m"
	imp := "package a\nimport _ \"example.com/m/b\"\n"

	if _, inc := Analyze(mod, "a/a_test.go", imp); inc {
		t.Error("_test.go must be excluded from architecture edges")
	}
	if _, inc := Analyze(mod, "a/tools.go", "//go:build ignore\n\n"+imp); inc {
		t.Error("//go:build ignore must be excluded")
	}
	if _, inc := Analyze(mod, "a/a.go", "//go:build neverdefined_tag\n\n"+imp); inc {
		t.Error("file with an unsatisfied build tag must be excluded")
	}
}

func TestAnalyzeIgnoresExternal(t *testing.T) {
	dirs, _ := Analyze("example.com/m", "a/a.go", "package a\nimport (\n\t\"fmt\"\n\t\"github.com/x/y\"\n)\n")
	if len(dirs) != 0 {
		t.Errorf("stdlib/external imports must not map to components: %v", dirs)
	}
}

func TestParseModulePath(t *testing.T) {
	if got := ParseModulePath("module example.com/x\n\ngo 1.25\n"); got != "example.com/x" {
		t.Errorf("ParseModulePath = %q", got)
	}
}
