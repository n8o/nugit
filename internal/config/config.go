// Package config reads .nugit/config.yml and applies it to the engine. Until
// now config.yml was inert; this makes its knobs real — notably c4.mode, which
// powers warn-until-ratified adoption (a freshly bootstrapped model warns
// instead of failing CI until a human ratifies it to enforce).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// Peers are sibling repos in the same organization whose knowledge is
	// readable from here (ADR-0032, phase 1: read-only, local paths only).
	Peers PeerList `yaml:"peers"`
	// Org declares this repo's own identity within the organization (ADR-0033).
	Org struct {
		// Repo is the stable org-wide id other repos name this one by in a
		// contract's `parties:`. Deliberately NOT a peer name: a peer name is
		// the READER's private label for a sibling and can change at will, while
		// a party id is a bilateral fact both repos must spell identically.
		// Empty (or malformed) means contract checking is INERT — nugit never
		// guesses which party this repo is from the remote, directory, or module.
		Repo string `yaml:"repo"`
	} `yaml:"org"`
	// Contracts configures cross-repo obligation checking (ADR-0033).
	Contracts struct {
		Mode string `yaml:"mode"` // warn (default) | fail | off
	} `yaml:"contracts"`
}

// Peer is one sibling repo's store, read-only.
type Peer struct {
	// Name is the display namespace a foreign object is qualified with
	// (`platform:ADR-0020`). Short, unique, [a-z0-9-].
	Name string `yaml:"name"`
	// Path is the peer's local checkout, relative to the nugit root (or absolute).
	// Phase 1 never fetches: a path that is not checked out contributes nothing.
	Path string `yaml:"path"`
}

// Dir resolves the peer's checkout against the nugit root.
func (p Peer) Dir(repoDir string) string {
	if filepath.IsAbs(p.Path) {
		return p.Path
	}
	return filepath.Join(repoDir, p.Path)
}

// PeerList decodes the `peers:` block WITHOUT ever failing the whole config.
// A malformed block, or one malformed entry inside a good block, degrades to
// "no peers" / "that entry is skipped" — federation is additive context, so it
// must never be able to take config.yml (and with it c4.mode, fail_on, the
// capture mode) down with it. Same fail-closed discipline as the enum knobs
// above: an unusable value never silently becomes a mode nobody asked for.
type PeerList []Peer

// UnmarshalYAML implements the lenient decode. It returns nil error always by
// design; see the type doc.
func (l *PeerList) UnmarshalYAML(n *yaml.Node) error {
	*l = nil
	if n.Kind != yaml.SequenceNode {
		return nil // scalar/mapping authored where a list belongs: no peers
	}
	for _, item := range n.Content {
		var p Peer
		if item.Decode(&p) != nil {
			continue // one bad entry never voids the good ones
		}
		*l = append(*l, p)
	}
	return nil
}

// peerNameRe is the peer-name grammar: a short lowercase namespace safe to
// prefix onto an id (`platform:ADR-0020`) and safe in a file/JSON context.
var peerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// validPeers filters the decoded list to the entries that can actually be used:
// a well-formed unique name and a non-empty path. Rejected entries are dropped
// silently-but-closed (doctor is where they become visible), never promoted to
// a config error.
func validPeers(in PeerList) PeerList {
	seen := map[string]bool{}
	var out PeerList
	for _, p := range in {
		p.Name = strings.TrimSpace(p.Name)
		p.Path = strings.TrimSpace(p.Path)
		if !peerNameRe.MatchString(p.Name) || p.Path == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	return out
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
	c.Contracts.Mode = "warn"
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
	c.Peers = validPeers(c.Peers)
	// This repo's org-wide identity (ADR-0033). A malformed id degrades to ""
	// (inert), never to a guess: binding this repo to the WRONG party's
	// obligations is strictly worse than checking nothing, and doctor makes the
	// absence visible.
	c.Org.Repo = strings.TrimSpace(c.Org.Repo)
	if !orgRepoRe.MatchString(c.Org.Repo) {
		c.Org.Repo = ""
	}
	c.Contracts.Mode = strings.ToLower(strings.TrimSpace(c.Contracts.Mode))
	switch c.Contracts.Mode {
	case "fail", "off":
	default:
		// Unknown value: fall back to the DEFAULT (warn), not to strict. Unlike
		// c4.mode and -fail-on — whose strict state is this repo's own declared
		// policy — a typo here must never silently hand another repo the power
		// to fail this repo's build (ADR-0033 point 7).
		c.Contracts.Mode = "warn"
	}
	return c, nil
}

// orgRepoRe is the party-id grammar: a stable org-wide repo id, optionally
// namespaced (`myorg/consumer-gateway`). Lowercase and punctuation-limited so
// two repos cannot "agree" on ids that differ only in case or whitespace. The
// empty string matches nothing, which is how an absent id stays inert.
var orgRepoRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)

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

// ContractsOn reports whether cross-repo obligation checking runs at all
// (ADR-0033). It needs BOTH an explicit mode that isn't `off` and a configured
// identity — with no identity nugit cannot know which party it is, and guessing
// is the one thing it must never do.
func (c Config) ContractsOn() bool { return c.Contracts.Mode != "off" && c.Org.Repo != "" }

// ContractsFail reports whether an unmet obligation is a `fail` finding rather
// than the default `warn` (the ADR-0016 candidate-lane ramp).
func (c Config) ContractsFail() bool { return c.Contracts.Mode == "fail" }
