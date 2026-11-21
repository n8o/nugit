// Package verify is the commit verification gate (ADR-0056). It has two
// halves, joined by the local usage log the way ADR-0042 joins context()
// calls to edits:
//
//   - Evidence is a Claude Code hook. When the repo's verify skill, a review,
//     or the test suite runs in a session, it appends an evidence record.
//   - Decide runs in the commit-msg hook. It finds the session's evidence,
//     stamps the commit `verified: <kinds>` (or `verified: none`), and under
//     verify.gate: enforce stops an unverified commit that gives no reason.
//
// Nothing here calls a model or the network. What the evidence means (whether
// the verify skill is any good) is the repo's business; nugit supplies the
// slot, the gate and the record.
package verify

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/trailers"
	"github.com/n8o/nugit/internal/usage"
)

// Gate modes.
const (
	Off     = "off"
	Observe = "observe"
	Enforce = "enforce"
)

// ReleaseAfter is how many blocked commits in one session release the next
// one. The gate is a heuristic over hook records, and an agent it wrongly
// stops must not be stuck (the Claude Code team's rule, adopted in ADR-0056).
const ReleaseAfter = 2

// Options carries the clock and environment, so tests drive both.
type Options struct {
	NugitDir string // nugit root relative to the checkout ("." default)
	Now      time.Time
	Getenv   func(string) string
	// Config, when set, is used instead of reading the governing config.
	Config *config.Config
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now
}

func (o Options) getenv(k string) string {
	if o.Getenv == nil {
		return os.Getenv(k)
	}
	return o.Getenv(k)
}

// hookInput is the subset of Claude Code's hook stdin Evidence reads, for
// PostToolUse and UserPromptSubmit alike.
type hookInput struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	Prompt        string          `json:"prompt"`
}

type toolInput struct {
	Skill        string `json:"skill"`         // Skill
	Command      string `json:"command"`       // Bash
	SubagentType string `json:"subagent_type"` // Agent
}

// Evidence classifies one hook event and, when it is a verification, appends
// an evidence record. It returns the record it appended (nil when the event
// was not a verification). Like every nugit hook it can never break the tool
// call: any failure records nothing.
func Evidence(r io.Reader, opt Options) (rec *usage.Record) {
	defer func() {
		if recover() != nil {
			rec = nil
		}
	}()
	b, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil
	}
	var in hookInput
	if json.Unmarshal(b, &in) != nil {
		return nil
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	root, err := gitutil.Repo{Dir: cwd}.WorktreeRoot()
	if err != nil || root == "" {
		return nil
	}
	cfg, err := gateConfig(filepath.Join(root, nugitDir(opt)), opt)
	if err != nil || cfg.Verify.Gate == Off || cfg.Usage.Log == "off" {
		return nil
	}
	kind, via := Classify(in.HookEventName, in.ToolName, in.ToolInput, in.ToolResponse, in.Prompt, cfg)
	if kind == "" {
		return nil
	}
	session := strings.TrimSpace(in.SessionID)
	if session == "" {
		session = strings.TrimSpace(opt.getenv(usage.SessionEnv))
	}
	out := usage.Record{
		Time: opt.now(), Source: usage.SourceEvidence,
		Branch:  gitutil.Repo{Dir: root}.CurrentBranch(),
		Session: session, AgentSession: strings.TrimSpace(opt.getenv(usage.AgentSessionEnv)),
		Evidence: kind, Via: via,
	}
	if usage.Append(root, out) != nil {
		return nil
	}
	return &out
}

// Classify names the verification a hook event represents ("" = none):
//
//   - PostToolUse on Skill: a skill matching verify.skills is e2e, one
//     matching verify.reviews is review.
//   - PostToolUse on Agent: a subagent type matching verify.reviews is review.
//   - PostToolUse on Bash: a command with a segment that starts with one of
//     verify.tests is tests. PostToolUse is the success event; a failed call
//     goes to PostToolUseFailure, which nugit does not subscribe to. A
//     response that nonetheless reports a non-zero exit is not evidence.
//   - UserPromptSubmit: a typed `/<skill>` matching either list. A typed
//     slash command does not go through the Skill tool, so this is the only
//     place it is seen.
func Classify(event, tool string, rawIn, rawResp json.RawMessage, prompt string, cfg config.Config) (kind, via string) {
	switch event {
	case "UserPromptSubmit":
		p := strings.TrimSpace(prompt)
		if !strings.HasPrefix(p, "/") {
			return "", ""
		}
		name := strings.TrimPrefix(strings.Fields(p)[0], "/")
		return skillKind(name, cfg), name
	case "PostToolUse":
	default:
		return "", ""
	}
	var ti toolInput
	_ = json.Unmarshal(rawIn, &ti)
	switch tool {
	case "Skill":
		return skillKind(ti.Skill, cfg), ti.Skill
	case "Agent", "Task":
		if Matches(ti.SubagentType, cfg.VerifyReviews()) {
			return trailers.VerifiedReview, ti.SubagentType
		}
	case "Bash":
		if failed(rawResp) {
			return "", ""
		}
		if prefix := TestCommand(ti.Command, cfg.VerifyTests()); prefix != "" {
			return trailers.VerifiedTests, prefix
		}
	}
	return "", ""
}

func skillKind(name string, cfg config.Config) string {
	switch {
	case name == "":
		return ""
	case Matches(name, cfg.VerifySkills()):
		return trailers.VerifiedE2E
	case Matches(name, cfg.VerifyReviews()):
		return trailers.VerifiedReview
	}
	return ""
}

// Matches reports whether a skill or agent name matches one of the entries:
// its last segment (after ':' or '/') equals an entry, starts with it plus
// "-", or ends with "-" plus it.
func Matches(name string, entries []string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexAny(name, ":/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return false
	}
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" && (name == e || strings.HasPrefix(name, e+"-") || strings.HasSuffix(name, "-"+e)) {
			return true
		}
	}
	return false
}

// TestCommand returns the configured prefix a shell command runs, or "". The
// command is split on && || ; | and newlines, and leading `cd x`, env
// assignments and `time` are skipped, so `cd api && GOFLAGS=-count=1 go test
// ./...` counts.
func TestCommand(cmd string, prefixes []string) string {
	seps := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	for _, seg := range strings.Split(seps.Replace(cmd), "\n") {
		fields := strings.Fields(seg)
		for len(fields) > 0 && (strings.Contains(fields[0], "=") || fields[0] == "time" || fields[0] == "command") {
			fields = fields[1:]
		}
		line := strings.Join(fields, " ")
		for _, p := range prefixes {
			p = strings.Join(strings.Fields(p), " ")
			if p != "" && (line == p || strings.HasPrefix(line, p+" ")) {
				return p
			}
		}
	}
	return ""
}

// failed reports whether a Bash tool_response says the command exited non-zero
// or was interrupted. The documented PostToolUse response has no exit-code
// field, so this only catches the "Exit code N" text a failure carries.
func failed(raw json.RawMessage) bool {
	var resp struct {
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
		Interrupted bool   `json:"interrupted"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return false
	}
	if resp.Interrupted {
		return true
	}
	for _, s := range []string{resp.Stdout, resp.Stderr} {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "Exit code ") && !strings.HasPrefix(s, "Exit code 0") {
			return true
		}
	}
	return false
}

// Commit is the commit-msg hook's view of one commit.
type Commit struct {
	Message      string // the message file's contents
	Session      string // Claude Code session id ("" = not an agent session)
	AgentSession string // agent-deck instance id
	Branch       string
	Time         time.Time
}

// Decision is the gate's verdict on one commit.
type Decision struct {
	Mode    string
	Verdict string   // a usage.Verdict*, or "" when the gate did not apply
	Kinds   []string // the evidence kinds that back the commit
	// Stamp is the `verified:` line to add, "" when the message already has
	// one or the gate did not apply.
	Stamp string
	// Message is for stderr; "" says nothing.
	Message string
	Block   bool
}

// Decide applies verify.gate to a commit (ADR-0056).
//
// The gate applies only to an agent session: a commit with neither a Claude
// Code nor an agent-deck session id is a person at a terminal, and nugit has
// no evidence about how they checked their work. They can write the line.
//
// Evidence counts when it is from this session and no edit the pre-edit hook
// recorded for the session came after it: a check that ran before the tree
// changed verified a different tree. With no pre-edit hook there are no edit
// records, so any evidence in the session counts.
func Decide(cfg config.Config, c Commit, records []usage.Record) Decision {
	d := Decision{Mode: cfg.Verify.Gate}
	if d.Mode == Off || (c.Session == "" && c.AgentSession == "") {
		return d
	}
	mine := func(r usage.Record) bool {
		return (c.Session != "" && r.Session == c.Session) ||
			(c.AgentSession != "" && r.AgentSession == c.AgentSession)
	}
	var lastEdit time.Time
	for _, r := range records {
		if r.Source == usage.SourceHook && mine(r) && !r.Time.After(c.Time) && r.Time.After(lastEdit) {
			lastEdit = r.Time
		}
	}
	stale := 0
	for _, r := range records {
		if r.Source != usage.SourceEvidence || !mine(r) || r.Time.After(c.Time) {
			continue
		}
		if r.Time.Before(lastEdit) {
			stale++
			continue
		}
		if !has(d.Kinds, r.Evidence) {
			d.Kinds = append(d.Kinds, r.Evidence)
		}
	}
	written := trailers.ParseVerified(trailers.Parse(body(c.Message)).Verified)
	hasLine := len(written.Kinds) > 0 || written.Note != ""

	switch {
	case len(d.Kinds) > 0:
		d.Verdict = usage.VerdictVerified
		if !hasLine {
			d.Stamp = "verified: " + strings.Join(d.Kinds, ", ")
		}
		return d
	case written.Explained():
		d.Verdict = usage.VerdictExplained
		return d
	}

	why := "this session recorded no verify skill, review or test run"
	if stale > 0 {
		why = fmt.Sprintf("this session's last verification ran before its latest edit (%d stale)", stale)
	}
	if !hasLine {
		d.Stamp = "verified: " + trailers.VerifiedNone
	}
	if d.Mode == Observe {
		d.Verdict = usage.VerdictWouldBlock
		return d
	}
	// enforce: two blocks since the session's last commit release the next.
	blocks := 0
	for _, r := range records {
		if r.Source != usage.SourceCommit || !mine(r) || r.Time.After(c.Time) {
			continue
		}
		if r.Verdict == usage.VerdictBlock {
			blocks++
		} else {
			blocks = 0
		}
	}
	if blocks >= ReleaseAfter {
		d.Verdict = usage.VerdictRelease
		if !hasLine {
			d.Stamp = fmt.Sprintf("verified: none — released after %d blocked commits", blocks)
		}
		d.Message = fmt.Sprintf("nugit: %s; letting this commit through after %d blocks so the session is not stuck (verify.gate: enforce, ADR-0056)\n", why, blocks)
		return d
	}
	d.Verdict = usage.VerdictBlock
	d.Block = true
	d.Stamp = ""
	d.Message = "nugit: commit blocked (verify.gate: enforce): " + why + ".\n" +
		"  Run the repo's verify skill, a review, or the tests, then commit again;\n" +
		"  or say why it was not verified with a line in the message, e.g.\n" +
		"    verified: none — docs-only change\n" +
		fmt.Sprintf("  After %d blocks in this session the next commit goes through.\n", ReleaseAfter)
	return d
}

// CommitMsg is the commit-msg hook's half (ADR-0056): decide, stamp the
// message file, record the verdict, and return the hook's exit code. It
// never blocks a commit on its own failure: only a deliberate enforce verdict
// returns non-zero.
func CommitMsg(dir, msgFile string, stderr io.Writer, opt Options) (code int) {
	defer func() {
		if recover() != nil {
			code = 0
		}
	}()
	cfg, err := gateConfig(dir, opt)
	if err != nil || cfg.Verify.Gate == Off {
		return 0
	}
	b, err := os.ReadFile(msgFile)
	if err != nil {
		return 0
	}
	repo := gitutil.Repo{Dir: dir}
	c := Commit{
		Message:      string(b),
		Session:      strings.TrimSpace(opt.getenv(usage.SessionEnv)),
		AgentSession: strings.TrimSpace(opt.getenv(usage.AgentSessionEnv)),
		Branch:       repo.CurrentBranch(),
		Time:         opt.now(),
	}
	if c.Session == "" && c.AgentSession == "" {
		return 0
	}
	if strings.TrimSpace(stripComments(c.Message)) == "" {
		return 0 // an empty message aborts the commit anyway
	}
	var recs []usage.Record
	if cfg.Usage.Log != "off" {
		recs, _ = usage.Read(dir)
	}
	d := Decide(cfg, c, recs)
	if d.Verdict == "" {
		return 0
	}
	if d.Stamp != "" {
		_ = os.WriteFile(msgFile, []byte(appendTrailer(c.Message, d.Stamp)), 0o644)
	}
	if cfg.Usage.Log != "off" {
		_ = usage.Append(dir, usage.Record{
			Time: c.Time, Source: usage.SourceCommit, Branch: c.Branch,
			Session: c.Session, AgentSession: c.AgentSession,
			Gate: d.Mode, Verdict: d.Verdict, Kinds: d.Kinds,
		})
	}
	if d.Message != "" {
		fmt.Fprint(stderr, d.Message)
	}
	if d.Block {
		return 1
	}
	return 0
}

// gateConfig is the governing config (ADR-0055): verify.gate comes from the
// default branch, so a branch cannot turn its own gate off.
func gateConfig(nugitRoot string, opt Options) (config.Config, error) {
	if opt.Config != nil {
		return *opt.Config, nil
	}
	repo := gitutil.Repo{Dir: nugitRoot}
	return config.LoadLocal(nugitRoot, func() (string, bool) {
		return repo.DefaultBranchFile(".nugit/config.yml", 500*time.Millisecond)
	})
}

func nugitDir(opt Options) string {
	if opt.NugitDir == "" {
		return "."
	}
	return opt.NugitDir
}

func body(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return stripComments(msg[i+1:])
	}
	return ""
}

func stripComments(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "#") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// appendTrailer adds line to the message's trailer paragraph (or starts one),
// above git's "#" comment block so an editor commit does not strip it.
func appendTrailer(msg, line string) string {
	lines := strings.SplitAfter(msg, "\n")
	cut := len(lines)
	for cut > 0 {
		l := strings.TrimSpace(lines[cut-1])
		if l != "" && !strings.HasPrefix(l, "#") {
			break
		}
		cut--
	}
	content := strings.TrimRight(strings.Join(lines[:cut], ""), "\n")
	comments := strings.Join(lines[cut:], "")
	paras := strings.Split(content, "\n\n")
	last := paras[len(paras)-1]
	sep := "\n\n"
	if len(paras) > 1 && allTrailers(last) {
		sep = "\n"
	}
	return content + sep + line + "\n" + comments
}

func allTrailers(para string) bool {
	for _, l := range strings.Split(para, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !trailers.IsTrailerLine(l) && !strings.Contains(l, ": ") {
			return false
		}
	}
	return true
}

func has(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
