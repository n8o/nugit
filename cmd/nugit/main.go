// Command nugit is the thin-keystone CLI from the re-shaped plan: it renders the
// unified PR view from a repo's own committed artifacts. No index, no
// content-addressing, no embeddings, no merge driver — those are deferred until
// the keystone proves pull (see PLAN.md).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/consistency"
	"github.com/n8o/nugit/internal/distill"
	"github.com/n8o/nugit/internal/doctor"
	"github.com/n8o/nugit/internal/engine"
	"github.com/n8o/nugit/internal/localmem"
	"github.com/n8o/nugit/internal/mcp"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/render"
	"github.com/n8o/nugit/internal/retrieval"
	"github.com/n8o/nugit/internal/scaffold"
	"github.com/n8o/nugit/internal/trailers"
)

const usage = `nugit — git-native PR view (thin keystone)

usage:
  nugit init [flags]          scaffold .nugit/ and bootstrap a C4 model
  nugit context [flags]       scoped, typed knowledge bundle for a path (for agents)
  nugit mcp [flags]           run the MCP stdio server (exposes context() to agents)
  nugit distill [flags]       promote commit-trailer decisions/lessons to durable knowledge
  nugit c4 render [flags]      render the C4 model as Mermaid
  nugit explain [check]       rationale + remediation for a consistency check
  nugit pr-render [flags]      compute & render the unified PR view
  nugit version

context flags:
  -C dir         repo directory (default ".")
  -path p        file or dir the agent is operating on (required)
  -task t        task description for keyword matching
  -budget n      token budget (default 4000)
  -format f      markdown (default) | json

init flags:
  -C dir            repo directory (default ".")
  -mode m           c4 enforcement written to config: warn (default) | enforce
  -layout l         model layout: cmake (C++ edges) | container|toplevel|flat
                    (default: auto — Go import graph, else CMake, else structural)
  -component-dirs d comma-separated container dirs for structural layout
  -no-model         scaffold only; write a template workspace.dsl
  -force            overwrite existing .nugit files

pr-render flags:
  -C dir         repo directory (default ".")
  -base ref      base ref / target branch (default "HEAD~1")
  -head ref      head ref (default "HEAD")
  -format f      one of: markdown (default), check-run, json
  -fail-on sev   exit non-zero when findings reach this severity: fail|warn|none (default "fail")
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		os.Exit(cmdInit(os.Args[2:]))
	case "context":
		os.Exit(cmdContext(os.Args[2:]))
	case "mcp":
		os.Exit(cmdMCP(os.Args[2:]))
	case "remember":
		os.Exit(cmdRemember(os.Args[2:]))
	case "hook":
		os.Exit(cmdHook(os.Args[2:]))
	case "distill":
		os.Exit(cmdDistill(os.Args[2:]))
	case "c4":
		os.Exit(cmdC4(os.Args[2:]))
	case "explain":
		os.Exit(cmdExplain(os.Args[2:]))
	case "doctor":
		os.Exit(cmdDoctor(os.Args[2:]))
	case "pr-render":
		os.Exit(cmdPRRender(os.Args[2:]))
	case "version":
		fmt.Println("nugit 0.1.0-keystone")
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	mode := fs.String("mode", "warn", "c4 enforcement mode: warn|enforce")
	noModel := fs.Bool("no-model", false, "scaffold only; don't bootstrap a model")
	force := fs.Bool("force", false, "overwrite existing .nugit files")
	layout := fs.String("layout", "", "structural layout for any codebase: container|toplevel|flat (default: Go import graph, else structural)")
	componentDirs := fs.String("component-dirs", "", "comma-separated container dirs for structural layout (default: apps,libs,services,...)")
	_ = fs.Parse(args)

	var cdirs []string
	if *componentDirs != "" {
		for _, c := range strings.Split(*componentDirs, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cdirs = append(cdirs, c)
			}
		}
	}

	res, err := scaffold.Run(scaffold.Options{
		RepoDir: *dir, Force: *force, NoModel: *noModel, Mode: *mode,
		Layout: *layout, ComponentDirs: cdirs,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit init: %v\n", err)
		return 1
	}

	for _, f := range res.Created {
		fmt.Printf("  created  %s\n", f)
	}
	for _, f := range res.Skipped {
		fmt.Printf("  skipped  %s (exists; -force to overwrite)\n", f)
	}
	fmt.Println()
	switch {
	case *noModel && res.DSLCreated:
		fmt.Println("Scaffolded .nugit/ with a template workspace.dsl. Define your components + paths globs, then run `nugit pr-render`.")
	case *noModel:
		fmt.Println("Scaffolded .nugit/. A workspace.dsl already exists (left unchanged); use -force to replace it with a template.")
	case res.ModelEmpty:
		fmt.Println("No components found — wrote a template workspace.dsl. Define your components and paths globs by hand.")
	case res.WroteModel && res.Backend != "":
		fmt.Printf("Bootstrapped a C4 model from %s: %d component(s), %d dependency edge(s), in %s mode.\n",
			res.Backend, res.Components, res.Edges, res.Mode)
		fmt.Println("Next: review .nugit/architecture/workspace.dsl, then run `nugit pr-render`.")
		if res.Mode == "warn" {
			fmt.Println("When the model is ratified, set c4.mode to `enforce` in .nugit/config.yml.")
		}
	case res.WroteModel && res.Structural:
		fmt.Printf("Bootstrapped a STRUCTURAL model: %d component(s) from the directory layout, no relationships derived, in %s mode.\n",
			res.Components, res.Mode)
		fmt.Println("Review .nugit/architecture/workspace.dsl — declare relationships by hand, or add a per-language analyzer to enforce them. Then run `nugit pr-render`.")
	case res.WroteModel:
		fmt.Printf("Bootstrapped a C4 model: %d component(s), %d dependency edge(s), in %s mode.\n",
			res.Components, res.Edges, res.Mode)
		fmt.Println("Next: review .nugit/architecture/workspace.dsl, then run `nugit pr-render`.")
		if res.Mode == "warn" {
			fmt.Println("When the model is ratified, set c4.mode to `enforce` in .nugit/config.yml.")
		}
		if res.PolyglotHint {
			fmt.Println("Note: this repo has apps/libs subtrees the Go model doesn't cover — for a whole-repo (polyglot) model, run `nugit init -force -layout container`.")
		}
	case res.Structural && res.Components > 0:
		fmt.Printf("A workspace.dsl already exists (left unchanged); the layout has %d component(s). Use -force to regenerate.\n", res.Components)
	case res.Components > 0:
		fmt.Printf("A workspace.dsl already exists (left unchanged); discovered %d component(s), %d edge(s). Use -force to regenerate.\n",
			res.Components, res.Edges)
	}
	return 0
}

// cmdHook implements git hook entrypoints. `nugit hook commit-msg <file>`
// validates the trailer block per config capture.commit_msg (warn|block|off).
// cmdDistill promotes commit-trailer decisions/lessons into durable .nugit/ objects.
// cmdC4 renders the C4 model. `nugit c4 render -format mermaid`.
func cmdC4(args []string) int {
	if len(args) < 1 || (args[0] != "render" && args[0] != "gen-rules") {
		fmt.Fprintln(os.Stderr, "nugit c4: usage: nugit c4 (render|gen-rules) [-C dir] [flags]")
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("c4 "+sub, flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	format := fs.String("format", "mermaid", "render output: mermaid")
	out := fs.String("o", "", "gen-rules: write to this file instead of stdout")
	_ = fs.Parse(args[1:])
	cfg, _ := config.Load(*dir)
	dslPath := cfg.C4.DSL
	if dslPath == "" {
		dslPath = ".nugit/architecture/workspace.dsl"
	}
	src, err := os.ReadFile(*dir + "/" + dslPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit c4 %s: %v\n", sub, err)
		return 1
	}
	m := c4.Parse(string(src))
	if sub == "gen-rules" {
		cfgYML := c4.GenArchLint(m)
		if *out != "" {
			if err := os.WriteFile(*out, []byte(cfgYML), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "nugit c4 gen-rules: %v\n", err)
				return 1
			}
			fmt.Printf("Wrote go-arch-lint config to %s (%d components).\n", *out, len(m.Components))
			return 0
		}
		fmt.Print(cfgYML)
		return 0
	}
	switch *format {
	case "mermaid":
		fmt.Print(c4.Mermaid(m))
	default:
		fmt.Fprintf(os.Stderr, "nugit c4 render: unknown format %q\n", *format)
		return 2
	}
	return 0
}

// cmdDoctor runs the setup pre-flight (`nugit doctor`).
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	_ = fs.Parse(args)
	r := doctor.Run(*dir)
	for _, c := range r.Checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		fmt.Printf("  %s %s — %s\n", mark, c.Name, c.Detail)
	}
	if !r.AllOK() {
		fmt.Println("\nSome checks failed. Run `nugit init` or fix the items above.")
		return 1
	}
	fmt.Println("\nnugit is set up correctly here.")
	return 0
}

// cmdExplain prints the rationale + remediation for a consistency check.
func cmdExplain(args []string) int {
	if len(args) == 0 {
		fmt.Println("consistency checks (nugit explain <check>):")
		for _, c := range consistency.AllChecks() {
			fmt.Printf("  %s\n", c)
		}
		return 0
	}
	s, ok := consistency.Explain(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "nugit explain: unknown check %q (try `nugit explain`)\n", args[0])
		return 2
	}
	fmt.Printf("%s\n\n%s\n", args[0], s)
	return 0
}

func cmdDistill(args []string) int {
	fs := flag.NewFlagSet("distill", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	base := fs.String("base", "HEAD~1", "base ref")
	head := fs.String("head", "HEAD", "head ref")
	min := fs.Int("min-recur", 2, "min recurrences for a lesson to promote")
	_ = fs.Parse(args)
	res, err := distill.Distill(distill.Options{RepoDir: *dir, Base: *base, Head: *head, MinRecur: *min})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit distill: %v\n", err)
		return 1
	}
	for _, p := range res.Decisions {
		fmt.Printf("  promoted decision  %s\n", p)
	}
	for _, p := range res.Lessons {
		fmt.Printf("  promoted lesson    %s\n", p)
	}
	if len(res.Decisions) == 0 && len(res.Lessons) == 0 {
		fmt.Printf("Nothing to promote (%d already in the store).\n", res.Skipped)
	} else {
		fmt.Printf("\nPromoted %d decision(s), %d lesson(s). Review and commit them with the PR.\n", len(res.Decisions), len(res.Lessons))
	}
	return 0
}

// cmdRemember writes (or lists) ephemeral working-memory notes in .nugit-local/.
func cmdRemember(args []string) int {
	fs := flag.NewFlagSet("remember", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	text := fs.String("text", "", "the note to remember (required unless -list)")
	kind := fs.String("kind", "note", "note | lesson | decision")
	scope := fs.String("scope", "", "component scope")
	kw := fs.String("keywords", "", "comma-separated keywords")
	list := fs.Bool("list", false, "list recent entries instead of writing")
	n := fs.Int("n", 20, "how many to list")
	_ = fs.Parse(args)
	if *list {
		for _, e := range localmem.Recent(*dir, *n) {
			fmt.Printf("  [%s] %s: %s\n", e.Time, e.Kind, e.Text)
		}
		return 0
	}
	if strings.TrimSpace(*text) == "" {
		fmt.Fprintln(os.Stderr, "nugit remember: -text is required (or -list)")
		return 2
	}
	var kws []string
	for _, k := range strings.Split(*kw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			kws = append(kws, k)
		}
	}
	if err := localmem.Append(*dir, localmem.Entry{Kind: *kind, Text: *text, Scope: *scope, Keywords: kws}); err != nil {
		fmt.Fprintf(os.Stderr, "nugit remember: %v\n", err)
		return 1
	}
	fmt.Println("Remembered (ephemeral, .nugit-local/ — gitignored). Promote durable items via a commit trailer + `nugit distill`.")
	return 0
}

func cmdHook(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "nugit hook: usage: nugit hook commit-msg <file>")
		return 2
	}
	switch args[0] {
	case "commit-msg":
		fs := flag.NewFlagSet("hook commit-msg", flag.ContinueOnError)
		dir := fs.String("C", ".", "nugit root (relative to the git toplevel)")
		if fs.Parse(args[1:]) != nil {
			return 0 // never block a commit on a flag parse error
		}
		rest := fs.Args()
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "nugit hook commit-msg: missing message file")
			return 2
		}
		cfg, _ := config.Load(*dir)
		if cfg.Capture.CommitMsg == "off" {
			return 0
		}
		b, err := os.ReadFile(rest[0])
		if err != nil {
			return 0 // never block a commit on a hook read error
		}
		// the body is everything after the subject line
		msg := string(b)
		body := msg
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			body = msg[i+1:]
		}
		warns := trailers.Validate(trailers.Parse(body))
		if len(warns) == 0 {
			return 0
		}
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "nugit: %s\n", w)
		}
		if cfg.Capture.CommitMsg == "block" {
			fmt.Fprintln(os.Stderr, "nugit: commit blocked (capture.commit_msg: block). Fix the trailer or remove the block.")
			return 1
		}
		return 0 // warn mode: advise, don't block
	default:
		fmt.Fprintf(os.Stderr, "nugit hook: unknown hook %q\n", args[0])
		return 2
	}
}

func cmdMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	_ = fs.Parse(args)
	if err := mcp.Serve(*dir, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "nugit mcp: %v\n", err)
		return 1
	}
	return 0
}

func cmdContext(args []string) int {
	fs := flag.NewFlagSet("context", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	path := fs.String("path", "", "file or dir the agent is operating on (required)")
	task := fs.String("task", "", "task description for keyword matching")
	budget := fs.Int("budget", 0, "token budget (default 4000)")
	format := fs.String("format", "markdown", "output: markdown|json")
	_ = fs.Parse(args)
	if *path == "" {
		fmt.Fprintln(os.Stderr, "nugit context: -path is required")
		return 2
	}
	b, err := retrieval.Context(retrieval.Options{
		RepoDir: *dir, Path: *path, Task: *task, BudgetTokens: *budget,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit context: %v\n", err)
		return 1
	}
	switch *format {
	case "json":
		out, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit context: %v\n", err)
			return 1
		}
		fmt.Println(string(out))
	default:
		fmt.Print(b.Markdown())
	}
	return 0
}

func cmdPRRender(args []string) int {
	fs := flag.NewFlagSet("pr-render", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	base := fs.String("base", "HEAD~1", "base ref / target branch")
	head := fs.String("head", "HEAD", "head ref")
	format := fs.String("format", "markdown", "output format: markdown|check-run|json")
	failOn := fs.String("fail-on", "", "exit non-zero at severity: fail|warn|none (default from config.yml, else fail)")
	_ = fs.Parse(args)

	// An unset -fail-on takes its default from config.yml (pr_render.fail_on).
	if *failOn == "" {
		if cfg, err := config.Load(*dir); err == nil {
			*failOn = cfg.PRRender.FailOn
		} else {
			*failOn = "fail"
		}
	}

	switch *failOn {
	case "fail", "warn", "none":
	default:
		fmt.Fprintf(os.Stderr, "nugit: unknown -fail-on %q (want fail|warn|none)\n", *failOn)
		return 2
	}

	rep, err := engine.BuildReport(engine.Options{RepoDir: *dir, Base: *base, Head: *head})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit: %v\n", err)
		return 1
	}

	switch *format {
	case "markdown":
		fmt.Print(render.Markdown(rep))
	case "check-run":
		b, err := render.CheckRunJSON(rep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
	case "json":
		b, err := render.StructuredJSON(rep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
	default:
		fmt.Fprintf(os.Stderr, "nugit: unknown format %q\n", *format)
		return 2
	}

	return exitCode(rep, *failOn)
}

// exitCode maps findings onto a process exit code per the -fail-on policy.
func exitCode(rep model.Report, failOn string) int {
	switch failOn {
	case "none":
		return 0
	case "warn":
		if c := render.Conclusion(rep); c == "failure" || c == "neutral" {
			return 1
		}
	default: // "fail"
		if render.Conclusion(rep) == "failure" {
			return 1
		}
	}
	return 0
}
