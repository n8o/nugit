// Package narrative is the OPT-IN, cached LLM prose layer (§9.5). It is inert by
// default: Generate returns "" — and makes no network call — unless narrative is
// explicitly enabled, the PR is architectural, and an API key is present. So the
// deterministic render path is byte-identical when off, and the facts the prose
// summarizes always come from the deterministic deltas (it never replaces them).
package narrative

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/model"
)

const defaultModel = "claude-haiku-4-5-20251001"

// Generate returns an opt-in LLM summary, or "" when disabled / not architectural
// / no API key / on any error. It NEVER fails the render and NEVER calls out when
// disabled.
func Generate(rep model.Report, repoDir string, enabled bool, modelName string) string {
	if !enabled || rep.Significance.Tier != model.TierArchitectural {
		return ""
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return ""
	}
	if modelName == "" {
		modelName = defaultModel
	}
	prompt := buildPrompt(rep)
	// Key on the ref pair too: the cache dir is shared across all refs, so without
	// this PR A's summary could be served for PR B that shares commit subjects.
	h := hashStr(modelName + "\n" + rep.BaseRef + ".." + rep.HeadRef + "\n" + prompt)
	if cached, ok := readCache(repoDir, h); ok {
		return cached
	}
	out, err := callAnthropic(key, modelName, prompt)
	if err != nil || strings.TrimSpace(out) == "" {
		return "" // degrade silently to the deterministic view
	}
	out = strings.TrimSpace(out)
	writeCache(repoDir, h, out)
	return out
}

// buildPrompt assembles a facts-only prompt from the deterministic report.
func buildPrompt(rep model.Report) string {
	var f strings.Builder
	fmt.Fprintf(&f, "Significance: %s (%s)\n", rep.Significance.Tier, strings.Join(rep.Significance.Reasons, "; "))
	if len(rep.Commits) > 0 {
		f.WriteString("Commits:\n")
		for _, c := range rep.Commits {
			fmt.Fprintf(&f, "- %s\n", c.Subject)
		}
	}
	if len(rep.Findings) > 0 {
		f.WriteString("Consistency findings:\n")
		for _, fd := range rep.Findings {
			fmt.Fprintf(&f, "- [%s] %s: %s\n", fd.Severity, fd.Check, fd.Title)
		}
	}
	// The architecture delta is the most important fact to summarize — and it makes
	// the prompt (and thus the cache key) distinguish materially different PRs.
	c4 := rep.C4
	if n := len(c4.AddedComponents) + len(c4.RemovedComponents) + len(c4.ChangedComponents) +
		len(c4.AddedRels) + len(c4.RemovedRels) + len(c4.ChangedRels); n > 0 {
		f.WriteString("Architecture (C4) delta:\n")
		for _, c := range c4.AddedComponents {
			fmt.Fprintf(&f, "- + component %s\n", c.ID)
		}
		for _, c := range c4.RemovedComponents {
			fmt.Fprintf(&f, "- - component %s\n", c.ID)
		}
		for _, r := range c4.AddedRels {
			fmt.Fprintf(&f, "- + relationship %s -> %s\n", r.Src, r.Dst)
		}
		for _, r := range c4.RemovedRels {
			fmt.Fprintf(&f, "- - relationship %s -> %s\n", r.Src, r.Dst)
		}
		fmt.Fprintf(&f, "  (%d component change(s) total)\n", len(c4.ChangedComponents)+len(c4.ChangedRels))
	}
	if len(rep.Knowledge.Changes) > 0 {
		fmt.Fprintf(&f, "Knowledge objects changed: %d\n", len(rep.Knowledge.Changes))
	}
	return "You are summarizing a pull request for a reviewer. Using ONLY the facts " +
		"below (do not invent anything not present), write 2-3 sentences focused on the " +
		"architectural intent and any risk a reviewer should weigh. Be concrete and terse.\n\n" +
		f.String()
}

func callAnthropic(apiKey, modelName, prompt string) (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model":      modelName,
		"max_tokens": 320,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

func hashStr(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func cacheDir(repoDir string) string {
	return filepath.Join(repoDir, ".nugit", ".cache", "narrative")
}

func readCache(repoDir, h string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(cacheDir(repoDir), h+".txt"))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func writeCache(repoDir, h, content string) {
	d := cacheDir(repoDir)
	if os.MkdirAll(d, 0o755) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(d, h+".txt"), []byte(content), 0o644)
}
