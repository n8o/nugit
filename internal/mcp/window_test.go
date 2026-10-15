package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/retrieval"
	"github.com/n8o/nugit/internal/usage"
)

// bigDecision is a realistically sized component-scoped decision: a title, a
// few paragraphs, and a Rejected section — the shape that, ~80 at a time,
// produced a ~96,000-character bundle on a large pilot repo (ADR-0043).
func bigDecision(id, scope, extraFM, rejected string) string {
	body := strings.Repeat("The render pipeline keeps output deterministic so reviewers can diff it. ", 6)
	if rejected == "" {
		rejected = strings.Repeat("Streaming output per finding was rejected: reviewers lose the summary line. ", 4)
	}
	return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: " + scope +
		"\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\n" + extraFM +
		"provenance:\n  commit: x\n---\n\n# " + id + " keep the render pipeline deterministic\n\n" +
		body + "\n\n## Rejected\n\n" + rejected + "\n"
}

// bigStore: 100 decisions scoped to one component, 3 bound to the exact file,
// 20 global, plus lessons and references — far more than one bundle should hold.
func bigStore(t *testing.T, rejected string) string {
	dir := t.TempDir()
	wf(t, dir, ".nugit/architecture/workspace.dsl", wtDSL)
	for i := 0; i < 100; i++ {
		wf(t, dir, fmt.Sprintf(".nugit/decisions/c%03d.md", i),
			bigDecision(fmt.Sprintf("ADR-C%03d", i), "render", "", rejected))
	}
	for i := 0; i < 3; i++ {
		wf(t, dir, fmt.Sprintf(".nugit/decisions/p%d.md", i),
			bigDecision(fmt.Sprintf("ADR-Z%d-PATH", i), "global",
				"applies_to_paths:\n  - \"internal/render/render.go\"\n", rejected))
	}
	for i := 0; i < 20; i++ {
		wf(t, dir, fmt.Sprintf(".nugit/decisions/g%02d.md", i),
			bigDecision(fmt.Sprintf("ADR-G%02d", i), "global", "", rejected))
	}
	for i := 0; i < 30; i++ {
		wf(t, dir, fmt.Sprintf(".nugit/lessons/l%02d.md", i),
			"---\nschema_version: 1\nid: LESSON-"+fmt.Sprint(i)+"\ntype: lesson\nscope: render\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# a render lesson worth knowing\n")
	}
	return dir
}

// callContext drives one tools/call over the stdio loop and returns the
// result text exactly as a client receives it.
func callContext(t *testing.T, dir, args string) string {
	t.Helper()
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"context","arguments":` + args + `}}` + "\n"
	var out bytes.Buffer
	if err := Serve(dir, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.Result.Content) != 1 {
		t.Fatalf("bad reply %s: %v", out.String(), err)
	}
	return resp.Result.Content[0].Text
}

func TestMCPResultFitsTheWindowOnABigStore(t *testing.T) {
	dir := bigStore(t, "")
	for _, budget := range []int{24000, 100000} {
		text := callContext(t, dir, fmt.Sprintf(`{"path":"internal/render/render.go","budget_tokens":%d}`, budget))
		if len(text) >= MaxResultChars {
			t.Fatalf("budget %d: result is %d chars, must be < %d", budget, len(text), MaxResultChars)
		}
		var b retrieval.Bundle
		if err := json.Unmarshal([]byte(text), &b); err != nil {
			t.Fatal(err)
		}
		t.Logf("budget %d -> %d chars, %d decisions, omitted %v, budget %d, notices %q",
			budget, len(text), len(b.Decisions), b.Omitted, b.BudgetTokens, b.Notices)
		if len(b.Decisions) != retrieval.DefaultMaxDecisions {
			t.Errorf("budget %d: want the %d-decision relevance cap, got %d", budget, retrieval.DefaultMaxDecisions, len(b.Decisions))
		}
		// Exact-path bindings first, then component scope; globals never
		// displace either.
		for i := 0; i < 3; i++ {
			if !b.Decisions[i].PathBound {
				t.Errorf("budget %d: decision %d is %s, want a path-bound decision first", budget, i, b.Decisions[i].ID)
			}
		}
		for _, d := range b.Decisions[3:] {
			if d.Scope != "render" {
				t.Errorf("budget %d: %s (scope %s) displaced a component-scoped decision", budget, d.ID, d.Scope)
			}
		}
		if b.Omitted["decision"] != 123-retrieval.DefaultMaxDecisions {
			t.Errorf("budget %d: omitted %v, want %d decisions", budget, b.Omitted, 123-retrieval.DefaultMaxDecisions)
		}
		if !b.Clamped || b.RequestedBudgetTokens != budget || b.BudgetTokens > MaxBudgetTokens {
			t.Errorf("budget %d: want clamped to <= %d, got clamped=%v requested=%d budget=%d",
				budget, MaxBudgetTokens, b.Clamped, b.RequestedBudgetTokens, b.BudgetTokens)
		}
		joined := strings.Join(b.Notices, "\n")
		if !strings.Contains(joined, "more in-scope decisions omitted") ||
			!strings.Contains(joined, fmt.Sprintf("budget clamped from %d to", budget)) {
			t.Errorf("budget %d: footer notices missing: %q", budget, b.Notices)
		}
	}
	recs, err := usage.Read(dir)
	if err != nil || len(recs) != 2 {
		t.Fatalf("usage: want 2 records, got %d (%v)", len(recs), err)
	}
	for _, r := range recs {
		if !r.Clamped || r.Chars == 0 || r.Chars >= MaxResultChars || r.Omitted == 0 {
			t.Errorf("usage record must carry the delivery facts: %+v", r)
		}
	}
}

// The token estimate counts content runes; JSON escapes `<`, `>` and `&` to six
// characters each. A store full of `a -> b` arrows and `<T>` generics can blow
// past the char ceiling under a budget that "fits" — so the ceiling must be
// enforced on the bytes, not inferred from the estimate.
func TestMCPCeilingHoldsWhenEscapingInflatesTheBytes(t *testing.T) {
	dir := bigStore(t, strings.Repeat("<T> -> <U> && ", 120))
	text := callContext(t, dir, `{"path":"internal/render/render.go","budget_tokens":24000}`)
	if len(text) >= MaxResultChars {
		t.Fatalf("result is %d chars, must be < %d", len(text), MaxResultChars)
	}
	var b retrieval.Bundle
	if err := json.Unmarshal([]byte(text), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Decisions) == 0 || !b.Decisions[0].PathBound {
		t.Errorf("the shrunk bundle must still lead with the exact-path decision: %+v", b.Decisions)
	}
	if b.BudgetTokens >= MaxBudgetTokens {
		t.Errorf("escaping-heavy bundle should have been shrunk below the %d clamp, got %d", MaxBudgetTokens, b.BudgetTokens)
	}
}

// A small store is untouched: nothing clamped at the default budget, nothing
// omitted, no notices.
func TestMCPSmallStoreUnchanged(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/architecture/workspace.dsl", wtDSL)
	wf(t, dir, ".nugit/decisions/r.md", decisionMD("ADR-R"))
	var b retrieval.Bundle
	if err := json.Unmarshal([]byte(callContext(t, dir, `{"path":"internal/render/render.go"}`)), &b); err != nil {
		t.Fatal(err)
	}
	if b.Clamped || b.Truncated || len(b.Notices) > 0 || len(b.Omitted) > 0 ||
		b.BudgetTokens != retrieval.DefaultBudgetTokens || len(b.Decisions) != 1 {
		t.Errorf("small store must be served exactly as before: %+v", b)
	}
}
