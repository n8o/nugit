package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/render"
)

const tierADR = "---\nschema_version: 1\nid: ADR-T\ntype: decision\nscope: compa\nstatus: %s\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# ADR-T\n"

// Evidence tiers surface in the pr-render knowledge bullet, and the signals
// (enforce mode, backend) shift them: enforced in enforce mode, checked in
// warn mode, declared when nothing binds the object, proposed pre-ratification.
func TestKnowledgeTiersInMarkdown(t *testing.T) {
	run := func(t *testing.T, config, adr, wantBullet string) {
		t.Helper()
		dir, base := seed(t, true)
		if config != "" {
			write(t, dir, ".nugit/config.yml", config)
		}
		write(t, dir, ".nugit/decisions/t.md", adr)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "add adr")
		rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: "HEAD"})
		if err != nil {
			t.Fatal(err)
		}
		md := render.Markdown(rep)
		if !strings.Contains(md, wantBullet) {
			t.Errorf("markdown must contain %q, got:\n%s", wantBullet, md)
		}
	}

	t.Run("enforce mode -> enforced", func(t *testing.T) {
		run(t, "", strings.Replace(tierADR, "%s", "accepted", 1), "(compa, accepted, enforced)")
	})
	t.Run("warn mode -> checked", func(t *testing.T) {
		run(t, "schema_version: 1\nc4:\n  mode: warn\n",
			strings.Replace(tierADR, "%s", "accepted", 1), "(compa, accepted, checked)")
	})
	t.Run("unbound global -> declared", func(t *testing.T) {
		adr := strings.Replace(tierADR, "scope: compa", "scope: global", 1)
		run(t, "", strings.Replace(adr, "%s", "accepted", 1), "(global, accepted, declared)")
	})
	t.Run("proposed -> proposed", func(t *testing.T) {
		run(t, "", strings.Replace(tierADR, "%s", "proposed", 1), "(compa, proposed, proposed)")
	})
}
