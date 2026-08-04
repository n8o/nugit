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
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/agentcfg"
	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/consistency"
	"github.com/n8o/nugit/internal/deploy"
	"github.com/n8o/nugit/internal/distill"
	"github.com/n8o/nugit/internal/doctor"
	"github.com/n8o/nugit/internal/engine"
	"github.com/n8o/nugit/internal/icepanel"
	"github.com/n8o/nugit/internal/localmem"
	"github.com/n8o/nugit/internal/mcp"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/modelfacts"
	"github.com/n8o/nugit/internal/notion"
	"github.com/n8o/nugit/internal/nudge"
	"github.com/n8o/nugit/internal/obsidian"
	"github.com/n8o/nugit/internal/ratify"
	"github.com/n8o/nugit/internal/reinforce"
	"github.com/n8o/nugit/internal/render"
	"github.com/n8o/nugit/internal/retrieval"
	"github.com/n8o/nugit/internal/scaffold"
	"github.com/n8o/nugit/internal/skillopt"
	"github.com/n8o/nugit/internal/trailers"
	usagelog "github.com/n8o/nugit/internal/usage"
)

const usage = `nugit — git-native PR view (thin keystone)

usage:
  nugit init [flags]          scaffold .nugit/ and bootstrap a C4 model
  nugit agent [flags]         print/install the MCP wiring config for a coding agent
  nugit context [flags]       scoped, typed knowledge bundle for a path (for agents)
  nugit mcp [flags]           run the MCP stdio server (exposes context() to agents)
  nugit stats [flags]         aggregate the local context() usage log
  nugit remember [flags]      jot ephemeral working memory (.nugit-local/, gitignored)
  nugit distill [flags]       promote commit-trailer decisions/lessons to durable knowledge (as proposed)
  nugit ratify [flags] <id>…  promote proposed knowledge objects to the ratified corpus (ADR-0016)
  nugit reinforce [flags] <id> append-only reinforcement of a recurring lesson/decision (ADR-0019)
  nugit hook commit-msg <f>   git hook entrypoint: validate the commit-trailer block
                              (capture.commit_msg: nudge also prompts on significant commits)
  nugit c4 render [flags]      render the C4 model as Mermaid
  nugit c4 gen-rules [flags]   generate go-arch-lint YAML from the C4 model
  nugit c4 export [flags]      export the C4 model (-format icepanel) as an import payload
  nugit c4 preview [flags]     live C4 diagrams via local Structurizr renderer (Docker)
  nugit deploy [flags]        deterministic deployable-container inventory (Dockerfiles + CMake)
  nugit model facts [flags]   deterministic grounding bundle for the bootstrap agent
  nugit doctor [flags]        setup pre-flight health checks
  nugit obsidian [flags]      (re)generate .nugit/INDEX.md for the Obsidian vault
  nugit notion publish [flags] publish the knowledge corpus to a Notion database
  nugit export [flags]        export the knowledge store as eval cases (JSONL, skill-optimizer)
  nugit explain [check]       rationale + remediation for a consistency check
  nugit pr-render [flags]      compute & render the unified PR view
  nugit version

agent flags:
  -C dir         repo directory (default ".")
  -client c      claude-code (default) | cursor | codex | opencode | generic
  -install       write the config file (claude-code only; others print the snippet)
  -force         with -install: overwrite an existing .mcp.json
  -bin path      nugit binary to embed (default "nugit", resolved from PATH)

context flags:
  -C dir         repo directory (default ".")
  -path p        file or dir the agent is operating on (required)
  -task t        task description for keyword matching
  -budget n      token budget (default 4000)
  -format f      markdown (default) | json

init flags:
  -C dir            repo directory (default ".")
  -mode m           c4 enforcement written to config: warn (default) | enforce
  -layout l         model layout: cmake|python|ts (language edges) | container|toplevel|flat
                    (default: auto — Go import graph, else CMake, else structural)
  -component-dirs d comma-separated container dirs for structural layout
  -no-model         scaffold only; write a template workspace.dsl
  -force            overwrite existing .nugit files

export flags:
  -C dir         repo directory (default ".")
  -format f      skillopt (default) — JSONL eval cases for a skill optimizer (ADR-0027)
  -o path        write the JSONL here instead of stdout
  -min-tier t    high-2 (default: emit thin triggers too) | high-3 (rich triggers only)
  -report path   write the full JSON report here (a summary always goes to stderr)

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
	case "agent":
		os.Exit(cmdAgent(os.Args[2:]))
	case "context":
		os.Exit(cmdContext(os.Args[2:]))
	case "mcp":
		os.Exit(cmdMCP(os.Args[2:]))
	case "stats":
		os.Exit(cmdStats(os.Args[2:]))
	case "notion":
		os.Exit(cmdNotion(os.Args[2:]))
	case "remember":
		os.Exit(cmdRemember(os.Args[2:]))
	case "hook":
		os.Exit(cmdHook(os.Args[2:]))
	case "distill":
		os.Exit(cmdDistill(os.Args[2:]))
	case "ratify":
		os.Exit(cmdRatify(os.Args[2:]))
	case "reinforce":
		os.Exit(cmdReinforce(os.Args[2:]))
	case "c4":
		os.Exit(cmdC4(os.Args[2:]))
	case "export":
		os.Exit(cmdExport(os.Args[2:]))
	case "explain":
		os.Exit(cmdExplain(os.Args[2:]))
	case "doctor":
		os.Exit(cmdDoctor(os.Args[2:]))
	case "deploy":
		os.Exit(cmdDeploy(os.Args[2:]))
	case "model":
		os.Exit(cmdModel(os.Args[2:]))
	case "obsidian":
		os.Exit(cmdObsidian(os.Args[2:]))
	case "pr-render":
		os.Exit(cmdPRRender(os.Args[2:]))
	case "version":
		fmt.Println(versionString())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// versionString reports the real installed version with no ldflags: `go install
// …@v0.1.0` stamps Main.Version, and a local build stamps the VCS revision/dirty
// flag — so `nugit version` always reflects what you actually have.
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "nugit (unknown)"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return "nugit " + v
	}
	rev, dirty := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev != "" {
		return "nugit (devel " + rev + dirty + ")"
	}
	return "nugit (devel)"
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	mode := fs.String("mode", "warn", "c4 enforcement mode: warn|enforce")
	noModel := fs.Bool("no-model", false, "scaffold only; don't bootstrap a model")
	force := fs.Bool("force", false, "overwrite existing .nugit files")
	layout := fs.String("layout", "", "model layout: cmake|python|ts (language edges) or container|toplevel|flat (structural; default: Go import graph, else CMake, else structural)")
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
	fmt.Println()
	fmt.Println("Wire your coding agent (exposes the context() MCP tool):")
	fmt.Println("  nugit agent -client claude-code -install     # writes .mcp.json in this repo")
	fmt.Println("  nugit agent -client cursor|codex|opencode    # prints the snippet + where it goes")
	return 0
}

// cmdAgent prints (or, for claude-code, installs) the MCP config that wires
// `nugit mcp` into a coding agent — the missing step between capture (which
// works out of the box) and retrieval (which stays dark until the client
// knows how to launch the server).
func cmdAgent(args []string) int {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	client := fs.String("client", "claude-code", "coding agent: claude-code|cursor|codex|opencode|generic")
	install := fs.Bool("install", false, "write the config file (claude-code only)")
	force := fs.Bool("force", false, "with -install: overwrite an existing .mcp.json")
	bin := fs.String("bin", "", "nugit binary to embed (default \"nugit\", resolved from PATH)")
	_ = fs.Parse(args)

	c := agentcfg.Client(*client)
	text, dest, err := agentcfg.Snippet(c, *dir, *bin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit agent: %v\n", err)
		return 2
	}
	if !*install {
		fmt.Print(text)
		fmt.Printf("\n→ put this in %s\n", dest)
		return 0
	}
	if c != agentcfg.ClaudeCode {
		fmt.Fprintf(os.Stderr, "nugit agent: -install only supports claude-code (its config is project-scoped); for %s, merge this snippet into %s yourself:\n\n%s", c, dest, text)
		return 2
	}
	created, path, err := agentcfg.InstallClaudeCode(*dir, *bin, *force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit agent: %v\n", err)
		return 1
	}
	if !created {
		fmt.Printf("  skipped  %s (exists — never merged automatically; -force to overwrite)\n\n", path)
		fmt.Printf("Merge this snippet manually:\n\n%s", text)
		return 0
	}
	fmt.Printf("  created  %s\n", path)
	fmt.Println("Restart Claude Code in this repo to pick up the nugit MCP server (context tool).")
	return 0
}

// cmdHook implements git hook entrypoints. `nugit hook commit-msg <file>`
// validates the trailer block per config capture.commit_msg
// (warn|nudge|block|off). In nudge mode it additionally prompts with a
// trailer stub when a significant staged change carries no capture (ADR-0023);
// the nudge is advisory only and never blocks.
// cmdDistill promotes commit-trailer decisions/lessons into durable .nugit/ objects.
// cmdC4 renders the C4 model. `nugit c4 render -format mermaid`.
func cmdC4(args []string) int {
	if len(args) < 1 || (args[0] != "render" && args[0] != "gen-rules" && args[0] != "export" && args[0] != "preview") {
		fmt.Fprintln(os.Stderr, "nugit c4: usage: nugit c4 (render|gen-rules|export|preview) [-C dir] [flags]")
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("c4 "+sub, flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	format := fs.String("format", "mermaid", "render: mermaid | export: icepanel")
	out := fs.String("o", "", "write to this file instead of stdout (gen-rules/export)")
	port := fs.String("port", "8080", "preview: localhost port for Structurizr Lite")
	_ = fs.Parse(args[1:])
	cfg, _ := config.Load(*dir)
	dslPath := cfg.C4.DSL
	if dslPath == "" {
		dslPath = ".nugit/architecture/workspace.dsl"
	}
	if sub == "preview" {
		return c4Preview(*dir, dslPath, *port)
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
	if sub == "export" {
		if *format != "icepanel" {
			fmt.Fprintf(os.Stderr, "nugit c4 export: unknown format %q (want: icepanel)\n", *format)
			return 2
		}
		data, err := json.MarshalIndent(icepanel.Transform(m), "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit c4 export: %v\n", err)
			return 1
		}
		if *out != "" {
			if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "nugit c4 export: %v\n", err)
				return 1
			}
			fmt.Printf("Wrote IcePanel import payload to %s (%d objects, %d connections).\n",
				*out, len(m.Components)+1, len(m.Relationships))
			return 0
		}
		fmt.Println(string(data))
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

// cmdObsidian (re)generates .nugit/INDEX.md so the corpus opens cleanly as an
// Obsidian vault — the no-sync editing surface for the knowledge layer.
func cmdObsidian(args []string) int {
	fs := flag.NewFlagSet("obsidian", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	_ = fs.Parse(args)
	md, err := obsidian.Generate(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit obsidian: %v\n", err)
		return 1
	}
	out := *dir + "/.nugit/INDEX.md"
	if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "nugit obsidian: %v\n", err)
		return 1
	}
	fmt.Printf("Wrote %s. Open the repo folder in Obsidian and start at INDEX.\n", out)
	return 0
}

// cmdDeploy runs the deterministic deployable-detector (SPEC-003): the container
// inventory derived from Dockerfiles + CMake install(RUNTIME), with confidence tiers
// and evidence — the ground truth the ADR-0012 bootstrap agent may not contradict.
func cmdDeploy(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	format := fs.String("format", "markdown", "output: markdown | json")
	_ = fs.Parse(args)
	containers, sum, err := deploy.Detect(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit deploy: %v\n", err)
		return 1
	}
	if *format == "json" {
		b, _ := json.MarshalIndent(map[string]any{"summary": sum, "containers": containers}, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("# Deployable containers: %d  (%d×HIGH-3, %d×HIGH-2, %d×NEEDS-AGENT)\n",
		sum.Containers, sum.High3, sum.High2, sum.NeedsAgent)
	fmt.Printf("_scanned %d Dockerfiles; excluded %d base/test; %d deploy-confirmed",
		sum.Dockerfiles, sum.BaseOrTest, sum.DeployConfirmed)
	if sum.FirstPartyRegistry != "" {
		fmt.Printf(" against %s (%d infra images excluded)", sum.FirstPartyRegistry, sum.InfraImages)
	}
	fmt.Print("_\n\n")
	fmt.Println("| container | lang | source | confidence | deployed | evidence |")
	fmt.Println("|---|---|---|---|---|---|")
	for _, c := range containers {
		dep := ""
		if c.DeployConfirmed {
			dep = "✓"
		}
		fmt.Printf("| %s | %s | %s | %s | %s | %s |\n",
			c.Name, c.Language, c.SourceDir, c.Confidence, dep, strings.Join(c.Evidence, "; "))
	}
	if len(sum.DeployedNotDetected) > 0 {
		fmt.Printf("\n_deployed first-party but not detected (agent's queue): %s_\n",
			strings.Join(sum.DeployedNotDetected, ", "))
	}
	return 0
}

// cmdModel emits the deterministic grounding the ADR-0012 bootstrap agent reasons over.
// `nugit model facts` — the deployable inventory + dependency edges + shared libs, as
// the ground truth the agent must not contradict when drafting workspace.dsl.
func cmdModel(args []string) int {
	if len(args) < 1 || args[0] != "facts" {
		fmt.Fprintln(os.Stderr, "nugit model: usage: nugit model facts [-C dir] [-format json|markdown]")
		return 2
	}
	fs := flag.NewFlagSet("model facts", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	format := fs.String("format", "json", "output: json | markdown")
	_ = fs.Parse(args[1:])
	b, err := modelfacts.Facts(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit model facts: %v\n", err)
		return 1
	}
	if *format == "markdown" {
		fmt.Printf("# Model grounding for %s\n\n", b.Repo)
		fmt.Printf("%d containers (%d×HIGH-3, %d×HIGH-2, %d×NEEDS-AGENT), %d dependency edges, %d shared libs.\n\n",
			b.Summary.Containers, b.Summary.High3, b.Summary.High2, b.Summary.NeedsAgent, len(b.Edges), len(b.Libs))
		fmt.Printf("_Agent: %s_\n", b.Instruction)
		return 0
	}
	out, _ := json.MarshalIndent(b, "", "  ")
	fmt.Println(string(out))
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
			if c.Advisory {
				mark = "-" // advisory: informative, never gates
			}
		}
		fmt.Printf("  %s %s — %s\n", mark, c.Name, c.Detail)
	}
	if h := r.Health; h != nil {
		fmt.Printf("\n  store health: %d/100\n", h.Score)
		for _, reason := range h.Reasons {
			fmt.Printf("    - %s\n", reason)
		}
		fmt.Printf("  %s\n", h.CountsLine())
	}
	if !r.AllOK() {
		fmt.Println("\nSome checks failed. Run `nugit init` or fix the items above.")
		return 1
	}
	fmt.Println("\nnugit is set up correctly here.")
	return 0
}

// c4Preview launches Structurizr's local renderer against the model directory — a
// git-native C4 viewer: it renders workspace.dsl live, no cloud, no sync. Uses the
// maintained `structurizr/structurizr local` image (the old `structurizr/lite` is
// EOL). Falls back to printing the command when Docker isn't installed.
func c4Preview(dir, dslPath, port string) int {
	modelDir, err := filepath.Abs(filepath.Join(dir, filepath.Dir(dslPath)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit c4 preview: %v\n", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(modelDir, filepath.Base(dslPath))); err != nil {
		fmt.Fprintf(os.Stderr, "nugit c4 preview: no %s under %s — run from the nugit root or pass -C\n", filepath.Base(dslPath), modelDir)
		return 1
	}
	// The renderer serves the directory that contains workspace.dsl.
	dockerArgs := []string{"run", "--rm", "-p", port + ":8080", "-v", modelDir + ":/usr/local/structurizr", "structurizr/structurizr", "local"}
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("Docker not found. Start the Structurizr local renderer manually:")
		fmt.Printf("  docker %s\n", strings.Join(dockerArgs, " "))
		fmt.Println("…then open http://localhost:" + port + "  (binaries: https://docs.structurizr.com/local)")
		return 0
	}
	fmt.Printf("Starting Structurizr (local) → http://localhost:%s  (Ctrl-C to stop)\n", port)
	c := exec.Command("docker", dockerArgs...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nugit c4 preview: %v\n", err)
		return 1
	}
	return 0
}

// cmdExport projects the knowledge store OUTBOUND as an external artifact
// (ADR-0027): today one format, `skillopt` — JSONL eval cases for a skill
// optimizer, so the benchmark corpus is a DERIVED artifact that regenerates
// with the store instead of a hand-mined snapshot that freezes.
//
// The JSONL goes to stdout (clean and pipeable); the tier/refusal summary goes
// to stderr, and -report writes the full per-object accounting as JSON.
func cmdExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	format := fs.String("format", "skillopt", "export format: skillopt")
	out := fs.String("o", "", "write the JSONL to this file instead of stdout")
	minTier := fs.String("min-tier", "high-2", "lowest tier to emit: high-2 | high-3")
	report := fs.String("report", "", "write the full JSON report to this file")
	_ = fs.Parse(args)
	if *format != "skillopt" {
		fmt.Fprintf(os.Stderr, "nugit export: unknown format %q (want: skillopt)\n", *format)
		return 2
	}
	cases, rep, err := skillopt.Export(skillopt.Options{RepoDir: *dir, MinTier: *minTier})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit export: %v\n", err)
		return 2
	}
	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit export: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}
	if err := skillopt.WriteJSONL(w, cases); err != nil {
		fmt.Fprintf(os.Stderr, "nugit export: %v\n", err)
		return 1
	}
	if *report != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*report, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "nugit export: %v\n", err)
			return 1
		}
	}
	fmt.Fprint(os.Stderr, rep.SummaryLines())
	return 0
}

// cmdExplain prints the rationale + remediation for a consistency check.
func cmdExplain(args []string) int {
	if len(args) == 0 {
		fmt.Println("consistency checks (nugit explain <check>):")
		for _, c := range consistency.AllChecks() {
			fmt.Printf("  %s\n", c)
		}
		fmt.Println("topics:")
		for _, tp := range consistency.AllTopics() {
			fmt.Printf("  %s\n", tp)
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
	min := fs.Int("min-recur", 1, "min recurrences for a lone learned: to promote (decision-bearing commits always promote; ADR-0018)")
	status := fs.String("status", "proposed", "minted status: proposed | ratified")
	_ = fs.Parse(args)
	res, err := distill.Distill(distill.Options{RepoDir: *dir, Base: *base, Head: *head, MinRecur: *min, Status: *status})
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
	} else if *status == "ratified" {
		fmt.Printf("\nPromoted %d decision(s), %d lesson(s). Review and commit them with the PR.\n", len(res.Decisions), len(res.Lessons))
	} else {
		fmt.Printf("\nPromoted %d decision(s), %d lesson(s) as proposed. Review, commit with the PR, then ratify with 'nugit ratify <id>'.\n", len(res.Decisions), len(res.Lessons))
	}
	return 0
}

// cmdRatify promotes proposed knowledge objects into the ratified corpus
// (ADR-0016): a surgical one-line status edit per object, or -list to show
// what's pending.
func cmdRatify(args []string) int {
	fs := flag.NewFlagSet("ratify", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	list := fs.Bool("list", false, "list proposed objects pending ratification")
	_ = fs.Parse(args)
	if *list {
		pending, err := ratify.Pending(*dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit ratify: %v\n", err)
			return 1
		}
		if len(pending) == 0 {
			fmt.Println("Nothing pending — no proposed objects in the store.")
			return 0
		}
		for _, o := range pending {
			fmt.Printf("  %-14s %-9s %s  %s\n", o.ID, o.Type, o.Created.Format("2006-01-02"), o.Path)
		}
		return 0
	}
	ids := fs.Args()
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "nugit ratify: usage: nugit ratify [-C dir] (-list | <id>...)")
		return 2
	}
	code := 0
	for _, id := range ids {
		res, err := ratify.Ratify(*dir, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit ratify: %v\n", err)
			code = 1
			continue
		}
		fmt.Printf("  ratified %s  %s -> %s  (%s)\n", res.ID, res.From, res.To, res.Path)
	}
	if code == 0 && len(ids) > 0 {
		fmt.Println("\nCommit the change with the PR — ratification lands via review (ADR-0011).")
	}
	return code
}

// cmdReinforce mints an append-only reinforcement of a live knowledge object
// whose failure class recurred (ADR-0019): a new file with a new id and a
// `reinforces:` edge — the target is never mutated (ADR-0003).
func cmdReinforce(args []string) int {
	fs := flag.NewFlagSet("reinforce", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	text := fs.String("text", "", "the reinforcement insight — what the recurrence taught (required)")
	kw := fs.String("keywords", "", "comma-separated keywords that widen retrieval of the target")
	status := fs.String("status", "proposed", "minted status: proposed | ratified")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "nugit reinforce: usage: nugit reinforce [-C dir] -text \"…\" [-keywords a,b] [-status proposed|ratified] <id>")
		return 2
	}
	var kws []string
	for _, k := range strings.Split(*kw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			kws = append(kws, k)
		}
	}
	res, err := reinforce.Reinforce(reinforce.Options{
		RepoDir: *dir, ID: fs.Arg(0), Text: *text, Keywords: kws, Status: *status,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit reinforce: %v\n", err)
		return 1
	}
	fmt.Printf("  reinforced %s  ->  %s (%s)\n", res.Target, res.ID, res.Path)
	if *status == "ratified" {
		fmt.Println("\nCommit the new file with the PR — the target object is untouched (ADR-0019).")
	} else {
		fmt.Println("\nCommit the new file with the PR, then promote it with `nugit ratify " + res.ID + "` — the target object is untouched (ADR-0019).")
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
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "nugit: %s\n", w)
		}
		if len(warns) > 0 && cfg.Capture.CommitMsg == "block" {
			fmt.Fprintln(os.Stderr, "nugit: commit blocked (capture.commit_msg: block). Fix the trailer or remove the block.")
			return 1
		}
		// ADR-0023: in nudge mode, prompt for capture when the staged change is
		// significant and the message has no trailer block. ForStagedCommit is
		// silent (returns "") in every other mode and on any internal error —
		// the nudge never blocks and never slows a commit.
		if txt := nudge.ForStagedCommit(*dir, msg, cfg); txt != "" {
			fmt.Fprint(os.Stderr, txt)
		}
		return 0 // warn/nudge mode: advise, don't block
	default:
		fmt.Fprintf(os.Stderr, "nugit hook: unknown hook %q\n", args[0])
		return 2
	}
}

// cmdNotion publishes the knowledge corpus OUTBOUND to a Notion database (ADR-0011).
// -dry-run (or a missing NOTION_TOKEN) prints the request bodies instead of POSTing.
func cmdNotion(args []string) int {
	if len(args) < 1 || args[0] != "publish" {
		fmt.Fprintln(os.Stderr, "nugit notion: usage: nugit notion publish -database <id> [-dry-run] [-C dir]")
		return 2
	}
	fs := flag.NewFlagSet("notion publish", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	database := fs.String("database", "", "target Notion database id (required)")
	dry := fs.Bool("dry-run", false, "print the request bodies instead of publishing")
	_ = fs.Parse(args[1:])
	if *database == "" {
		fmt.Fprintln(os.Stderr, "nugit notion publish: -database is required")
		return 2
	}
	docs, err := notion.CollectDocs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit notion publish: %v\n", err)
		return 1
	}
	token := os.Getenv("NOTION_TOKEN")
	if *dry || token == "" {
		if token == "" && !*dry {
			fmt.Fprintln(os.Stderr, "NOTION_TOKEN not set — dry run (request bodies only):")
		}
		for _, d := range docs {
			body, _ := json.MarshalIndent(notion.CreateBody(*database, d), "", "  ")
			fmt.Printf("# %s — POST /v1/pages\n%s\n\n", d.GitID, string(body))
		}
		fmt.Printf("(%d knowledge object(s) would be upserted into database %s)\n", len(docs), *database)
		return 0
	}
	res, err := notion.Publish(token, *database, docs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit notion publish: %v\n", err)
		return 1
	}
	fmt.Printf("Published to Notion: %d created, %d updated.\n", res.Created, res.Updated)
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "  error: %s\n", e)
	}
	if len(res.Errors) > 0 {
		return 1
	}
	return 0
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
	// Best-effort local usage log; a logging failure must never fail retrieval.
	_ = usagelog.Log(*dir, "cli", *task, b)
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

func cmdStats(args []string) int {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	since := fs.String("since", "", "window: a duration (e.g. 168h) or a date (YYYY-MM-DD); default all")
	format := fs.String("format", "markdown", "output: markdown|json")
	_ = fs.Parse(args)

	var cutoff time.Time
	if *since != "" {
		if d, err := time.ParseDuration(*since); err == nil {
			cutoff = time.Now().UTC().Add(-d)
		} else if t, err := time.Parse("2006-01-02", *since); err == nil {
			cutoff = t
		} else {
			fmt.Fprintf(os.Stderr, "nugit stats: -since must be a duration (168h) or date (YYYY-MM-DD), got %q\n", *since)
			return 2
		}
	}
	records, err := usagelog.Read(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nugit stats: %v\n", err)
		return 1
	}
	s := usagelog.Aggregate(records, cutoff)
	switch *format {
	case "json":
		out, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "nugit stats: %v\n", err)
			return 1
		}
		fmt.Println(string(out))
	default:
		fmt.Print(s.Markdown())
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

	// ADR-0026: remember whether -fail-on was explicit — an explicit flag weaker
	// than config is honored, but the report must surface the downgrade.
	explicitFailOn := *failOn

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

	rep, err := engine.BuildReport(engine.Options{RepoDir: *dir, Base: *base, Head: *head, FailOnFlag: explicitFailOn})
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
