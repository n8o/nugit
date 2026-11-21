package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// HeadKeys are the only config keys a change under review supplies for itself
// (ADR-0055). Each one describes the reviewed tree or how it is shown, never
// how hard a check bites:
//
//   - c4.dsl: where the model lives in the tree under review. A PR may move it.
//   - plan.scope: which plans the view renders. It never changes a verdict.
//   - retrieval.*: context() bundle sizes. pr-render does not read them.
//   - schema_version: the file's own format.
//
// Every other key is governed: read from the branch the change merges into.
// This is a list of head keys, not of governed ones, so a knob added later is
// governed until someone argues it out. That is the fail-closed direction.
var HeadKeys = []string{"schema_version", "c4.dsl", "plan.scope", "retrieval.*"}

// Govern returns the config a change is judged by: target's values for every
// governed key, head's for the HeadKeys.
func Govern(target, head Config) Config {
	g := target
	g.SchemaVersion = head.SchemaVersion
	g.C4.DSL = head.C4.DSL
	g.Plan.Scope = head.Plan.Scope
	g.Retrieval = head.Retrieval
	return g
}

// Change is one governed key whose value at head differs from the value the
// change is judged by.
type Change struct {
	Key    string `json:"key"`
	Target string `json:"target"`
	Head   string `json:"head"`
}

// GovernedChanges lists the governed keys whose value at head differs from
// target's, sorted by key. Head keys never appear: both sides carry head's
// value for them. Values are compared after LoadBytes normalized them, so a
// typo that normalizes to the same mode is not a change.
func GovernedChanges(target, head Config) []Change {
	a, b := flatten(Govern(target, head)), flatten(head)
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []Change
	for k := range keys {
		if a[k] != b[k] {
			out = append(out, Change{Key: k, Target: a[k], Head: b[k]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Governing is the config a check is judged by and where it came from.
type Governing struct {
	Config Config
	// FromTarget is true when the target's config governed. False means the
	// head's config did, for the reason in Note.
	FromTarget bool
	// Changes are the governed keys this change sets differently. When the
	// target had no usable config they are measured against the defaults.
	Changes []Change
	// Note says why head governed: "" when the target did, or when the target
	// could not be read at all (nothing to compare against, no finding).
	Note string
}

// Resolve applies ADR-0055 to raw bytes. head is the reviewed ref's
// config.yml. targetRead reports whether the target ref could be read at all,
// and target is its config.yml ("" when the file is absent there).
//
//   - target unreadable: head governs, nothing is compared. This is the
//     behaviour before ADR-0055, kept for a ref that cannot be resolved.
//   - target has no config, or one that does not parse: head governs (the
//     adoption PR, or a broken main that a PR has to be able to fix), and the
//     changes are measured against the defaults.
//   - otherwise: Govern(target, head).
//
// A head config that does not parse is an error, as it always was.
func Resolve(head []byte, target []byte, targetRead bool) (Governing, error) {
	hc, err := LoadBytes(head)
	if err != nil {
		return Governing{Config: Default()}, err
	}
	if !targetRead {
		return Governing{Config: hc}, nil
	}
	if strings.TrimSpace(string(target)) == "" {
		return Governing{Config: hc, Changes: GovernedChanges(Default(), hc),
			Note: "the target has no .nugit/config.yml"}, nil
	}
	tc, terr := LoadBytes(target)
	if terr != nil {
		return Governing{Config: hc, Changes: GovernedChanges(Default(), hc),
			Note: "the target's .nugit/config.yml does not parse"}, nil
	}
	return Governing{Config: Govern(tc, hc), FromTarget: true, Changes: GovernedChanges(tc, hc)}, nil
}

// LoadLocal is Load for the local hooks (ADR-0055): the checkout's config,
// with every governed key taken from target when target can be read. target
// returns the config.yml of the branch the work merges into (origin's
// default branch), and read=false when there is none to be had. The caller
// supplies it so this package stays free of git.
func LoadLocal(repoDir string, target func() (src string, read bool)) (Config, error) {
	head, err := os.ReadFile(filepath.Join(repoDir, ".nugit", "config.yml"))
	if err != nil && !os.IsNotExist(err) {
		return Default(), fmt.Errorf("reading config.yml: %w", err)
	}
	src, read := "", false
	if target != nil {
		src, read = target()
	}
	g, err := Resolve(head, []byte(src), read)
	return g.Config, err
}

// flatten renders a config as dotted key → value, from its yaml form, so a
// field added to Config is compared without anyone listing it here.
func flatten(c Config) map[string]string {
	b, err := yaml.Marshal(c)
	if err != nil {
		return nil
	}
	var m map[string]any
	if yaml.Unmarshal(b, &m) != nil {
		return nil
	}
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, vv := range t {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, vv)
			}
		case nil:
			out[prefix] = ""
		case []any:
			if len(t) == 0 {
				out[prefix] = "[]"
				return
			}
			js, _ := json.Marshal(t)
			out[prefix] = string(js)
		default:
			out[prefix] = fmt.Sprint(t)
		}
	}
	walk("", m)
	return out
}
