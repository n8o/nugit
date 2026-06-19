// Package localmem is the per-agent ephemeral working memory (§6): notes an agent
// jots mid-session into .nugit-local/ (gitignored, never committed), retrievable
// by context() within the workspace. Distinct from the durable .nugit/ store —
// this is scratch that a human/distiller later promotes, or discards. Append-only
// NDJSON, dependency-free.
package localmem

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const dir = ".nugit-local"
const journal = "journal.ndjson"

// Entry is one ephemeral note.
type Entry struct {
	Time     string   `json:"time"`
	Kind     string   `json:"kind"` // note | lesson | decision
	Text     string   `json:"text"`
	Scope    string   `json:"scope,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

// Append writes e (stamping Time if unset) to .nugit-local/journal.ndjson.
func Append(repoDir string, e Entry) error {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if e.Kind == "" {
		e.Kind = "note"
	}
	d := filepath.Join(repoDir, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(d, journal), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Recent returns up to max entries, newest first.
func Recent(repoDir string, max int) []Entry {
	f, err := os.Open(filepath.Join(repoDir, dir, journal))
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []Entry
	// bufio.Reader (not Scanner) so an oversized note line can't trip ErrTooLong
	// and silently drop every entry after it.
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadString('\n')
		if t := strings.TrimSpace(line); t != "" {
			var e Entry
			if json.Unmarshal([]byte(t), &e) == nil && e.Text != "" {
				all = append(all, e)
			}
		}
		if err != nil {
			break // io.EOF or read error: stop, but keep what parsed
		}
	}
	// newest first
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if max > 0 && len(all) > max {
		all = all[:max]
	}
	return all
}
