// Package config reads .nugit/config.yml and applies it to the engine. Until
// now config.yml was inert; this makes its knobs real — notably c4.mode, which
// powers warn-until-ratified adoption (a freshly bootstrapped model warns
// instead of failing CI until a human ratifies it to enforce).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config mirrors .nugit/config.yml (keystone subset).
type Config struct {
	SchemaVersion int `yaml:"schema_version"`
	C4            struct {
		DSL  string `yaml:"dsl"`
		Mode string `yaml:"mode"` // enforce | warn
	} `yaml:"c4"`
	Significance struct {
		TrivialMaxFiles int `yaml:"trivial_max_files"`
		TrivialMaxChurn int `yaml:"trivial_max_churn"`
	} `yaml:"significance"`
	PRRender struct {
		FailOn string `yaml:"fail_on"`
	} `yaml:"pr_render"`
	Capture struct {
		CommitMsg string `yaml:"commit_msg"` // warn (default) | block | off
	} `yaml:"capture"`
}

// Default returns the built-in defaults used when config.yml is absent or a key
// is omitted. A missing config defaults to enforce (strict) — `nugit init`
// explicitly writes warn for the adoption ramp.
func Default() Config {
	var c Config
	c.SchemaVersion = 1
	c.C4.DSL = ".nugit/architecture/workspace.dsl"
	c.C4.Mode = "enforce"
	c.Significance.TrivialMaxFiles = 2
	c.Significance.TrivialMaxChurn = 20
	c.PRRender.FailOn = "fail"
	c.Capture.CommitMsg = "warn"
	return c
}

// Load reads .nugit/config.yml from repoDir. A missing file yields the defaults
// with no error; a malformed file yields an error (never a silent fallback that
// flips enforcement mode).
func Load(repoDir string) (Config, error) {
	b, err := os.ReadFile(filepath.Join(repoDir, ".nugit", "config.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Default(), fmt.Errorf("reading config.yml: %w", err)
	}
	return LoadBytes(b)
}

// LoadBytes parses config from raw bytes (e.g. read at a git ref). Empty input
// yields the defaults; a parse error is surfaced rather than masked.
func LoadBytes(b []byte) (Config, error) {
	c := Default()
	if len(b) == 0 {
		return c, nil
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Default(), fmt.Errorf("invalid config.yml: %w", err)
	}
	// Normalize + re-assert defaults for omitted/blank fields.
	c.C4.Mode = strings.ToLower(strings.TrimSpace(c.C4.Mode))
	if c.C4.Mode != "warn" && c.C4.Mode != "enforce" {
		c.C4.Mode = "enforce" // unknown value: fail closed (strict)
	}
	if c.C4.DSL == "" {
		c.C4.DSL = ".nugit/architecture/workspace.dsl"
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
	c.Capture.CommitMsg = strings.ToLower(strings.TrimSpace(c.Capture.CommitMsg))
	if c.Capture.CommitMsg != "block" && c.Capture.CommitMsg != "off" {
		c.Capture.CommitMsg = "warn"
	}
	return c, nil
}

// C4Warn reports whether the c4<->code check should warn (adoption) rather than
// fail (ratified enforcement).
func (c Config) C4Warn() bool { return c.C4.Mode == "warn" }
