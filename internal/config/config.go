// Package config reads .nugit/config.yml and applies it to the engine. Until
// now config.yml was inert; this makes its knobs real — notably c4.mode, which
// powers warn-until-ratified adoption (a freshly bootstrapped model warns
// instead of failing CI until a human ratifies it to enforce).
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config mirrors .nugit/config.yml (keystone subset).
type Config struct {
	SchemaVersion int `yaml:"schema_version"`
	C4            struct {
		DSL          string `yaml:"dsl"`
		Mode         string `yaml:"mode"` // enforce | warn
		OrphanPolicy string `yaml:"orphan_policy"`
	} `yaml:"c4"`
	Significance struct {
		TrivialMaxFiles int `yaml:"trivial_max_files"`
		TrivialMaxChurn int `yaml:"trivial_max_churn"`
	} `yaml:"significance"`
	PRRender struct {
		FailOn string `yaml:"fail_on"`
	} `yaml:"pr_render"`
}

// Default returns the built-in defaults used when config.yml is absent or a key
// is omitted.
func Default() Config {
	var c Config
	c.SchemaVersion = 1
	c.C4.DSL = ".nugit/architecture/workspace.dsl"
	c.C4.Mode = "enforce"
	c.Significance.TrivialMaxFiles = 2
	c.Significance.TrivialMaxChurn = 20
	c.PRRender.FailOn = "fail"
	return c
}

// Load reads .nugit/config.yml from repoDir, overlaying any present keys onto
// the defaults. A missing or unreadable file yields the defaults (never an
// error — config is optional).
func Load(repoDir string) Config {
	c := Default()
	b, err := os.ReadFile(filepath.Join(repoDir, ".nugit", "config.yml"))
	if err != nil {
		return c
	}
	_ = yaml.Unmarshal(b, &c) // present keys overlay; malformed file falls back to defaults so far
	// Re-assert defaults for any field left empty/zero by an explicit blank.
	if c.C4.DSL == "" {
		c.C4.DSL = ".nugit/architecture/workspace.dsl"
	}
	if c.C4.Mode == "" {
		c.C4.Mode = "enforce"
	}
	if c.Significance.TrivialMaxFiles <= 0 {
		c.Significance.TrivialMaxFiles = 2
	}
	if c.Significance.TrivialMaxChurn <= 0 {
		c.Significance.TrivialMaxChurn = 20
	}
	if c.PRRender.FailOn == "" {
		c.PRRender.FailOn = "fail"
	}
	return c
}

// C4Warn reports whether the c4<->code check should warn (adoption) rather than
// fail (ratified enforcement).
func (c Config) C4Warn() bool { return c.C4.Mode == "warn" }
