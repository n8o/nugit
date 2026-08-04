package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// These tests are the real thing ADR-0033 is about: TWO stores on disk, a
// contract declared in one repo, and the OTHER repo's pr-render failing to be
// silent about its own half. Everything runs through BuildReport — no check is
// called directly — so the wiring (config → engine → peer read → check) is
// under test, not just the predicate.

// contractRecord writes a ratified two-party contract. Only the `me` party's
// obligations may ever be evaluated in the consumer repo.
func contractRecord(id string, musts ...string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: contract\nscope: global\n" +
		"status: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n" +
		"parties:\n" +
		"  - repo: producer-service\n" +
		"    must:\n" +
		"      - name: transport version pinned in the shared env file\n" +
		"        file: third_party/versions.env\n" +
		"        matches: '^TRANSPORT_TAG=v0\\.3\\.'\n" +
		"  - repo: consumer-gateway\n" +
		"    must:\n" + strings.Join(musts, "") +
		"---\n\n# " + id + " — the transport contract\n\nboth halves or neither.\n"
}

func mustEntry(name, file, pattern string) string {
	return "      - name: " + name + "\n        file: " + file + "\n        matches: '" + pattern + "'\n"
}

// consumerCfg is the consumer repo's config: it names the producer as a peer
// (ADR-0032) AND declares its own org-wide identity (ADR-0033).
func consumerCfg(peerDir, orgRepo, mode string) string {
	s := "schema_version: 1\nc4:\n  mode: enforce\npr_render:\n  fail_on: fail\n"
	if orgRepo != "" {
		s += "org:\n  repo: " + orgRepo + "\n"
	}
	if mode != "" {
		s += "contracts:\n  mode: " + mode + "\n"
	}
	if peerDir != "" {
		s += "peers:\n  - name: producer\n    path: " + peerDir + "\n"
	}
	return s
}

func contractFindings(rep model.Report) []model.Finding {
	var out []model.Finding
	for _, f := range rep.Findings {
		if f.Check == "contract-obligation" {
			out = append(out, f)
		}
	}
	return out
}

// commitAll stages and commits everything, returning the new head.
func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

// buildConsumer wires a consumer repo with a producer peer holding one contract.
func buildConsumer(t *testing.T, contract, orgRepo, mode string) (dir, peer, base string) {
	t.Helper()
	dir, base = seed(t, true)
	peer = t.TempDir()
	write(t, peer, ".nugit/contracts/transport.md", contract)
	write(t, dir, ".nugit/config.yml", consumerCfg(peer, orgRepo, mode))
	return dir, peer, base
}

// THE HEADLINE CASE: a peer declares a contract naming this repo; one of this
// repo's obligations is satisfied and one is not. The unmet one warns, and the
// finding names the contract, its origin, the must's own name, and the file —
// so a reader can act without opening the contract.
func TestPeerContractUnmetObligationWarnsWithFullProvenance(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard passes the standard protocol list", "apps/gateway/server.cpp", "useStandardProtocols"),
		mustEntry("gateway pins the transport tag", "apps/gateway/versions.env", `^TRANSPORT_TAG=v0\.3\.`))
	dir, _, base := buildConsumer(t, c, "consumer-gateway", "")
	// Half the contract is honored; the mirror guard is the armed footgun.
	write(t, dir, "apps/gateway/versions.env", "TRANSPORT_TAG=v0.3.7\n")
	write(t, dir, "apps/gateway/server.cpp", "auto f = makeRawFactory();\n")
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := contractFindings(rep)
	if len(fs) != 1 {
		t.Fatalf("want exactly the ONE unmet obligation, got %d: %+v", len(fs), fs)
	}
	f := fs[0]
	if f.Severity != model.SevWarn {
		t.Errorf("severity = %s, want warn by default (ADR-0033 point 7)", f.Severity)
	}
	line := f.Title + " " + f.Detail
	for _, want := range []string{
		"producer:CONTRACT-0001", // contract, qualified (ADR-0032)
		"peer producer",          // origin spelled out
		"mirror guard passes the standard protocol list", // the must, by NAME
		"apps/gateway/server.cpp",                        // the file
		"useStandardProtocols",                           // the pattern
		"consumer-gateway",                               // the party id that matched
	} {
		if !strings.Contains(line, want) {
			t.Errorf("finding must name %q; got:\n%s\n%s", want, f.Title, f.Detail)
		}
	}
	// The satisfied half must be silent — a check that reports met obligations
	// is noise nobody reads.
	if strings.Contains(line, "gateway pins the transport tag") {
		t.Error("a MET obligation must not be reported")
	}
}

// The producer's own obligations are visible in the contract this repo reads,
// and must never fire here: they are not this repo's business and cannot be
// fixed in this repo's PR.
func TestOtherPartiesObligationsNeverFire(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/server.cpp", "useStandardProtocols"))
	dir, _, base := buildConsumer(t, c, "consumer-gateway", "")
	write(t, dir, "apps/gateway/server.cpp", "useStandardProtocols(f);\n") // our half: met
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	// third_party/versions.env (the PRODUCER's obligation) does not exist here.
	// If other parties were evaluated, it would fire as UNMET.
	if fs := contractFindings(rep); len(fs) != 0 {
		t.Fatalf("another party's obligation fired in this repo: %+v", fs)
	}
}

// With no `org.repo` the check is inert. nugit does not guess which party a
// repo is from the remote, the directory name, or the module path: a wrong
// guess silently binds this repo to someone else's obligations.
func TestNoConfiguredIdentityIsInert(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/server.cpp", "useStandardProtocols"))
	dir, _, base := buildConsumer(t, c, "", "") // no org.repo
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := contractFindings(rep); len(fs) != 0 {
		t.Fatalf("with no org.repo the check must be inert, got: %+v", fs)
	}
}

// A malformed identity degrades to inert, not to a partial match.
func TestMalformedIdentityIsInert(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/server.cpp", "useStandardProtocols"))
	dir, _, base := buildConsumer(t, c, "Consumer Gateway!", "")
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := contractFindings(rep); len(fs) != 0 {
		t.Fatalf("a malformed org.repo must be inert, got: %+v", fs)
	}
}

// An unratified contract is a candidate (ADR-0016), not an obligation.
func TestUnratifiedPeerContractIsSilent(t *testing.T) {
	c := strings.Replace(contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/server.cpp", "useStandardProtocols")),
		"status: accepted", "status: proposed", 1)
	dir, _, base := buildConsumer(t, c, "consumer-gateway", "")
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := contractFindings(rep); len(fs) != 0 {
		t.Fatalf("a proposed contract must never fire: %+v", fs)
	}
}

// LESSON-read-from-reviewed-ref / ADR-0029: assertions read the REVIEWED REF,
// never the working tree. Both directions, because a check that reads disk is
// wrong in both — false negatives AND false positives.
func TestObligationIsEvaluatedAtTheReviewedRef(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/server.cpp", "useStandardProtocols"))

	t.Run("met at head, broken in the working tree -> still met", func(t *testing.T) {
		dir, _, base := buildConsumer(t, c, "consumer-gateway", "")
		write(t, dir, "apps/gateway/server.cpp", "useStandardProtocols(f);\n")
		head := commitAll(t, dir, "feat: guard")
		// Dirty the checkout AFTER the commit under review.
		write(t, dir, "apps/gateway/server.cpp", "auto f = makeRawFactory();\n")

		rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
		if err != nil {
			t.Fatal(err)
		}
		if fs := contractFindings(rep); len(fs) != 0 {
			t.Fatalf("the working tree changed the verdict — the check must read %s: %+v", head, fs)
		}
	})

	t.Run("broken at head, fixed in the working tree -> still unmet", func(t *testing.T) {
		dir, _, base := buildConsumer(t, c, "consumer-gateway", "")
		write(t, dir, "apps/gateway/server.cpp", "auto f = makeRawFactory();\n")
		head := commitAll(t, dir, "feat: no guard")
		write(t, dir, "apps/gateway/server.cpp", "useStandardProtocols(f);\n") // uncommitted "fix"

		rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
		if err != nil {
			t.Fatal(err)
		}
		if fs := contractFindings(rep); len(fs) != 1 {
			t.Fatalf("an uncommitted fix must not clear the finding, got %d: %+v", len(fs), fs)
		}
	})
}

// ADR-0032 identity: a peer's CONTRACT-0001 and a LOCAL CONTRACT-0001 are two
// different contracts. Neither shadows the other, and both render distinctly.
func TestPeerAndLocalContractsWithTheSameIDStayDistinct(t *testing.T) {
	peerC := contractRecord("CONTRACT-0001",
		mustEntry("peer-declared mirror guard", "apps/gateway/server.cpp", "useStandardProtocols"))
	dir, _, base := buildConsumer(t, peerC, "consumer-gateway", "")
	// A LOCAL contract that happens to mint the same id — the collision every
	// repo produces, because every repo starts numbering at 0001.
	write(t, dir, ".nugit/contracts/local.md", "---\nschema_version: 1\nid: CONTRACT-0001\ntype: contract\n"+
		"scope: global\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n"+
		"parties:\n  - repo: consumer-gateway\n    must:\n"+
		mustEntry("locally-declared health endpoint", "apps/gateway/health.cpp", "registerHealth")+
		"---\n\n# CONTRACT-0001 — the local one\n\nbody\n")
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := contractFindings(rep)
	if len(fs) != 2 {
		t.Fatalf("want 2 findings (one per distinct contract), got %d: %+v", len(fs), fs)
	}
	all := fs[0].Title + fs[0].Detail + fs[1].Title + fs[1].Detail
	for _, want := range []string{
		"producer:CONTRACT-0001", "peer-declared mirror guard",
		"locally-declared health endpoint",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the two same-id contracts collapsed: %q missing from\n%s", want, all)
		}
	}
	// The local one must render UNqualified — qualification is what tells a
	// reader which store a record came from.
	if !strings.Contains(fs[0].Title, "CONTRACT-0001 (local)") && !strings.Contains(fs[1].Title, "CONTRACT-0001 (local)") {
		t.Errorf("the local contract must render unqualified and labeled local:\n%s\n%s", fs[0].Title, fs[1].Title)
	}
}

// contracts.mode: off silences the check; an UNKNOWN value falls back to the
// default (warn), never to a mode nobody asked for — and specifically never to
// `fail`, because a typo must not hand another repo the power to redden this
// repo's build.
func TestContractsModeOffAndUnknownValue(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/server.cpp", "useStandardProtocols"))
	for _, tc := range []struct {
		mode    string
		want    int
		wantSev model.Severity
		whatFor string
	}{
		{"off", 0, "", "off must silence the check entirely"},
		{"fail", 1, model.SevFail, "fail must promote the finding"},
		{"warn", 1, model.SevWarn, "warn is the default severity"},
		{"lenient", 1, model.SevWarn, "an unknown value must fall back to warn, never to fail or off"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir, _, base := buildConsumer(t, c, "consumer-gateway", tc.mode)
			write(t, dir, "apps/gateway/server.cpp", "auto f = makeRawFactory();\n")
			head := commitAll(t, dir, "feat: gateway")

			rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
			if err != nil {
				t.Fatal(err)
			}
			fs := contractFindings(rep)
			if len(fs) != tc.want {
				t.Fatalf("%s: got %d findings, want %d: %+v", tc.whatFor, len(fs), tc.want, fs)
			}
			if tc.want > 0 && fs[0].Severity != tc.wantSev {
				t.Errorf("%s: severity = %s, want %s", tc.whatFor, fs[0].Severity, tc.wantSev)
			}
		})
	}
}

// A `must` whose file does not exist at the reviewed ref is UNMET, not an
// error: unverifiable is not met, and pr-render must still complete.
func TestMissingAssertedFileIsUnmetNotAnError(t *testing.T) {
	c := contractRecord("CONTRACT-0001",
		mustEntry("mirror guard present", "apps/gateway/does-not-exist.cpp", "useStandardProtocols"))
	dir, _, base := buildConsumer(t, c, "consumer-gateway", "")
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatalf("a missing asserted file must never error pr-render: %v", err)
	}
	fs := contractFindings(rep)
	if len(fs) != 1 {
		t.Fatalf("want 1 unmet obligation, got %d: %+v", len(fs), fs)
	}
	if !strings.Contains(fs[0].Detail, "does not exist here") {
		t.Errorf("the finding must say WHY it is unmet: %q", fs[0].Detail)
	}
}

// The whole feature is opt-in three times over. A repo with contracts.mode at
// its default but no identity and no peers must be byte-identical to before
// ADR-0033 — including never reading a peer store at all.
func TestUnconfiguredRepoIsUnaffected(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	head := commitAll(t, dir, "feat: a uses b")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := contractFindings(rep); len(fs) != 0 {
		t.Fatalf("a repo that never opted in produced contract findings: %+v", fs)
	}
}

// An absent peer still never fails pr-render, WITH contracts configured: CI
// checks out one repo, so "the sibling isn't here" stays the normal state and
// must degrade to "that peer declared nothing" (ADR-0032 point 3).
func TestAbsentPeerWithContractsConfiguredNeverFails(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/config.yml",
		consumerCfg(t.TempDir()+"/never-checked-out", "consumer-gateway", "fail"))
	head := commitAll(t, dir, "feat: gateway")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatalf("an absent peer must never error pr-render: %v", err)
	}
	for _, f := range rep.Findings {
		if f.Severity == model.SevFail {
			t.Errorf("an absent peer produced a FAIL finding: %s — %s", f.Check, f.Title)
		}
	}
}
