package consistency

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/model"
)

// GovernedConfigFindings reports a PR that changes a governed config key
// (ADR-0055). The change is legitimate work and does not fail the PR. But it
// changes the rules every later PR is judged by, so the reviewer should see it
// named as a policy change rather than find it in a YAML diff.
func GovernedConfigFindings(g config.Governing) []model.Finding {
	if len(g.Changes) == 0 {
		return nil
	}
	var b strings.Builder
	for _, c := range g.Changes {
		fmt.Fprintf(&b, "`%s`: %s → %s; ", c.Key, shown(c.Target), shown(c.Head))
	}
	list := strings.TrimSuffix(b.String(), "; ")
	if !g.FromTarget {
		return []model.Finding{{
			Check: "enforcement-config", Severity: model.SevWarn,
			Title: fmt.Sprintf("this PR sets %d governed config key(s), and they govern it: %s", len(g.Changes), g.Note),
			Detail: "Compared against the defaults: " + list + ". With no usable config on the target there is no " +
				"merged policy to keep, so this PR's own values apply (ADR-0055). Every later PR is judged by them.",
		}}
	}
	return []model.Finding{{
		Check: "enforcement-config", Severity: model.SevWarn,
		Title: fmt.Sprintf("this PR changes %d governed config key(s); they take effect only after it merges", len(g.Changes)),
		Detail: list + ". This PR is judged by the target's values, not its own: a change does not set the rules " +
			"that judge it (ADR-0055). Once merged, every later PR is judged by the new ones.",
	}}
}

func shown(v string) string {
	if v == "" {
		return "(unset)"
	}
	return "`" + v + "`"
}
