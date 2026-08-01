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
		CommitMsg string `yaml:"commit_msg"` // warn (default) | nudge | block | off
	} `yaml:"capture"`
	Narrative struct {
		Enabled bool   `yaml:"enabled"` // opt-in LLM prose; default false (off)
		Model   string `yaml:"model"`
	} `yaml:"narrative"`
	Usage struct {
		Log string `yaml:"log"` // on (default) | off — local .nugit/.cache/usage.jsonl only
	} `yaml:"usage"`
	Recurrence struct {
		Mode       string `yaml:"mode"`        // warn (default) | off
		WindowDays int    `yaml:"window_days"` // history window scanned for fix churn (default 90)
		MinFixes   int    `yaml:"min_fixes"`   // fix-typed commits that trigger the warn (default 3)
	} `yaml:"recurrence"`
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
	c.Usage.Log = "on"
	c.Recurrence.Mode = "warn"
	c.Recurrence.WindowDays = 90
	c.Recurrence.MinFixes = 3
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
	switch c.Capture.CommitMsg {
	case "nudge", "block", "off":
	default:
		// Unknown value: fall back to the default (warn), never to a mode the
		// user didn't ask for — a typo must not silently nudge, block, or
		// disable capture (ADR-0023 keeps this discipline).
		c.Capture.CommitMsg = "warn"
	}
	c.Usage.Log = strings.ToLower(strings.TrimSpace(c.Usage.Log))
	if c.Usage.Log != "off" {
		c.Usage.Log = "on"
	}
	c.Recurrence.Mode = strings.ToLower(strings.TrimSpace(c.Recurrence.Mode))
	if c.Recurrence.Mode != "off" {
		c.Recurrence.Mode = "warn"
	}
	if c.Recurrence.WindowDays <= 0 {
		c.Recurrence.WindowDays = 90
	}
	if c.Recurrence.MinFixes <= 0 {
		c.Recurrence.MinFixes = 3
	}
	return c, nil
}

// C4Warn reports whether the c4<->code check should warn (adoption) rather than
// fail (ratified enforcement).
func (c Config) C4Warn() bool { return c.C4.Mode == "warn" }

// FailOnRank orders -fail-on policies by strictness: none < warn < fail. An
// unknown value ranks strictest, so a typo can never read as a downgrade
// (ADR-0026: enforcement knobs must not silently cancel).
func FailOnRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return 0
	case "warn":
		return 1
	default:
		return 2
	}
}

// RecurrenceOn reports whether the recurrence check runs (ADR-0019).
func (c Config) RecurrenceOn() bool { return c.Recurrence.Mode != "off" }
