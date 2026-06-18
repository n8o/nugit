package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.C4.Mode != "enforce" || c.C4Warn() {
		t.Errorf("default mode should be enforce (not warn): %+v", c.C4)
	}
	if c.Significance.TrivialMaxFiles != 2 || c.Significance.TrivialMaxChurn != 20 {
		t.Errorf("default thresholds wrong: %+v", c.Significance)
	}
}

func TestLoadMissing(t *testing.T) {
	c := Load(t.TempDir()) // no .nugit/config.yml
	if c.C4.Mode != "enforce" || c.C4.DSL == "" {
		t.Errorf("missing config should yield defaults: %+v", c.C4)
	}
}

func TestLoadOverlay(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nugit"), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "schema_version: 1\nc4:\n  mode: warn\nsignificance:\n  trivial_max_files: 5\n"
	if err := os.WriteFile(filepath.Join(dir, ".nugit", "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Load(dir)
	if !c.C4Warn() {
		t.Errorf("mode warn should set C4Warn; got %+v", c.C4)
	}
	if c.Significance.TrivialMaxFiles != 5 {
		t.Errorf("overlay threshold = %d, want 5", c.Significance.TrivialMaxFiles)
	}
	if c.Significance.TrivialMaxChurn != 20 {
		t.Errorf("unset threshold should keep default 20, got %d", c.Significance.TrivialMaxChurn)
	}
	if c.C4.DSL == "" {
		t.Error("unset dsl should keep default")
	}
}
