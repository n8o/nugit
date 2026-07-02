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
	c, err := Load(t.TempDir()) // no .nugit/config.yml
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if c.C4.Mode != "enforce" || c.C4.DSL == "" {
		t.Errorf("missing config should yield defaults: %+v", c.C4)
	}
}

func TestLoadOverlay(t *testing.T) {
	c, err := LoadBytes([]byte("schema_version: 1\nc4:\n  mode: warn\nsignificance:\n  trivial_max_files: 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.C4Warn() {
		t.Errorf("mode warn should set C4Warn; got %+v", c.C4)
	}
	if c.Significance.TrivialMaxFiles != 5 {
		t.Errorf("overlay threshold = %d, want 5", c.Significance.TrivialMaxFiles)
	}
	if c.Significance.TrivialMaxChurn != 20 || c.C4.DSL == "" {
		t.Errorf("unset keys should keep defaults: %+v", c)
	}
}

func TestModeNormalizationAndValidation(t *testing.T) {
	// case/whitespace tolerated
	if c, _ := LoadBytes([]byte("c4:\n  mode: '  WARN  '\n")); !c.C4Warn() {
		t.Error("'  WARN  ' should normalize to warn")
	}
	// unknown value falls back to enforce (fail closed), not silently warn
	if c, _ := LoadBytes([]byte("c4:\n  mode: bogus\n")); c.C4.Mode != "enforce" {
		t.Errorf("unknown mode should fall back to enforce, got %q", c.C4.Mode)
	}
}

func TestMalformedSurfacesError(t *testing.T) {
	if _, err := LoadBytes([]byte("c4: : : not yaml\n  - broken")); err == nil {
		t.Error("malformed config.yml must surface an error, not silently default")
	}
}

func TestLoadReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nugit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nugit", "config.yml"), []byte("c4:\n  mode: warn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || !c.C4Warn() {
		t.Errorf("Load should read the file and parse warn; got %+v err=%v", c.C4, err)
	}
}

func TestUsageLogKnob(t *testing.T) {
	if c := Default(); c.Usage.Log != "on" {
		t.Errorf("default usage.log should be on, got %q", c.Usage.Log)
	}
	c, err := LoadBytes([]byte("usage:\n  log: OFF\n"))
	if err != nil || c.Usage.Log != "off" {
		t.Errorf("usage.log OFF should normalize to off; got %q err=%v", c.Usage.Log, err)
	}
	c, err = LoadBytes([]byte("usage:\n  log: bogus\n"))
	if err != nil || c.Usage.Log != "on" {
		t.Errorf("unknown usage.log should default to on; got %q err=%v", c.Usage.Log, err)
	}
}
