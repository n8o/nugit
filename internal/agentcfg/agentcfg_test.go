package agentcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func snippet(t *testing.T, client Client, repoDir, bin string) (string, string) {
	t.Helper()
	text, dest, err := Snippet(client, repoDir, bin)
	if err != nil {
		t.Fatalf("Snippet(%s): %v", client, err)
	}
	return text, dest
}

func TestSnippetGolden(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		client Client
		text   string
		dest   string // substring the destination hint must carry
	}{
		{ClaudeCode, "{\n" +
			"  \"mcpServers\": {\n" +
			"    \"nugit\": {\n" +
			"      \"command\": \"nugit\",\n" +
			"      \"args\": [\n        \"mcp\",\n        \"-C\",\n        \".\"\n      ]\n" +
			"    }\n  }\n}\n", ".mcp.json"},
		{Generic, "{\n" +
			"  \"mcpServers\": {\n" +
			"    \"nugit\": {\n" +
			"      \"command\": \"nugit\",\n" +
			"      \"args\": [\n        \"mcp\",\n        \"-C\",\n        \".\"\n      ]\n" +
			"    }\n  }\n}\n", ".mcp.json"},
		{Cursor, "{\n" +
			"  \"mcpServers\": {\n" +
			"    \"nugit\": {\n" +
			"      \"command\": \"nugit\",\n" +
			"      \"args\": [\n        \"mcp\",\n        \"-C\",\n        \"" + abs + "\"\n      ]\n" +
			"    }\n  }\n}\n", "~/.cursor/mcp.json"},
		{Codex, "[mcp_servers.nugit]\n" +
			"command = \"nugit\"\n" +
			"args = [\"mcp\", \"-C\", \"" + abs + "\"]\n", "~/.codex/config.toml"},
		{OpenCode, "{\n" +
			"  \"mcp\": {\n" +
			"    \"nugit\": {\n" +
			"      \"type\": \"local\",\n" +
			"      \"command\": [\n        \"nugit\",\n        \"mcp\",\n        \"-C\",\n        \".\"\n      ]\n" +
			"    }\n  }\n}\n", "opencode.json"},
	}
	for _, c := range cases {
		text, dest := snippet(t, c.client, dir, "")
		if text != c.text {
			t.Errorf("%s snippet:\n got: %q\nwant: %q", c.client, text, c.text)
		}
		if !strings.Contains(dest, c.dest) {
			t.Errorf("%s dest %q does not mention %q", c.client, dest, c.dest)
		}
	}
}

func TestSnippetUnknownClient(t *testing.T) {
	_, _, err := Snippet(Client("emacs"), t.TempDir(), "")
	if err == nil {
		t.Fatal("want error for unknown client")
	}
	for _, c := range Clients() {
		if !strings.Contains(err.Error(), string(c)) {
			t.Errorf("error %q does not list valid client %s", err, c)
		}
	}
}

// The claude-code snippet must stay structurally identical to the checked-in
// .mcp.json.example (modulo its _comment), or the two wiring paths drift.
func TestSnippetClaudeCodeMatchesExample(t *testing.T) {
	text, _ := snippet(t, ClaudeCode, t.TempDir(), "")
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("snippet is not valid JSON: %v", err)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", ".mcp.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	var example map[string]any
	if err := json.Unmarshal(b, &example); err != nil {
		t.Fatalf(".mcp.json.example is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(got["mcpServers"], example["mcpServers"]) {
		t.Errorf("mcpServers mismatch:\n got: %#v\nwant: %#v", got["mcpServers"], example["mcpServers"])
	}
}

// User-global configs (cursor, codex) must bake in the RESOLVED absolute repo
// dir — a relative "." would resolve against the client's own cwd.
func TestSnippetResolvesRelativeRepoDir(t *testing.T) {
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []Client{Cursor, Codex} {
		text, _ := snippet(t, client, ".", "")
		if !strings.Contains(text, abs) {
			t.Errorf("%s snippet does not contain resolved absolute dir %s:\n%s", client, abs, text)
		}
		if strings.Contains(text, `"-C", "."`) || strings.Contains(text, "\"-C\",\n        \".\"") {
			t.Errorf("%s snippet still carries a relative -C .:\n%s", client, text)
		}
	}
}

func TestSnippetBinOverride(t *testing.T) {
	text, _ := snippet(t, ClaudeCode, t.TempDir(), "/opt/bin/nugit")
	if !strings.Contains(text, `"command": "/opt/bin/nugit"`) {
		t.Errorf("bin override missing:\n%s", text)
	}
	text, _ = snippet(t, Codex, t.TempDir(), "/opt/bin/nugit")
	if !strings.Contains(text, `command = "/opt/bin/nugit"`) {
		t.Errorf("codex bin override missing:\n%s", text)
	}
}

func TestInstallClaudeCode(t *testing.T) {
	dir := t.TempDir()
	created, path, err := InstallClaudeCode(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first install: want created=true")
	}
	if path != filepath.Join(dir, ".mcp.json") {
		t.Errorf("path = %q", path)
	}
	want, _ := snippet(t, ClaudeCode, dir, "")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != want {
		t.Errorf("installed file != snippet:\n%s", first)
	}

	// Second call: created=false, file byte-unchanged.
	created, _, err = InstallClaudeCode(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second install: want created=false")
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(first) {
		t.Error("second install modified the file")
	}

	// Force overwrites (here: with a different bin, so bytes must change).
	created, _, err = InstallClaudeCode(dir, "/opt/bin/nugit", true)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("force install: want created=true")
	}
	forced, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(forced), "/opt/bin/nugit") {
		t.Errorf("force install did not overwrite:\n%s", forced)
	}
}

// A pre-existing .mcp.json with foreign content is NEVER touched without force
// — no merging, no clobbering.
func TestInstallPreservesForeignFile(t *testing.T) {
	dir := t.TempDir()
	foreign := `{"mcpServers":{"other":{"command":"other-tool"}}}` + "\n"
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	created, gotPath, err := InstallClaudeCode(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("want created=false on a foreign .mcp.json")
	}
	if gotPath != path {
		t.Errorf("path = %q, want %q", gotPath, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != foreign {
		t.Errorf("foreign .mcp.json was modified:\n%s", b)
	}
}
