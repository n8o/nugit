// Command nugit is the thin-keystone CLI from the re-shaped plan: it renders the
// unified PR view from a repo's own committed artifacts. No index, no
// content-addressing, no embeddings, no merge driver — those are deferred until
// the keystone proves pull (see PLAN.md).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/engine"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/render"
	"github.com/n8o/nugit/internal/scaffold"
)

const usage = `nugit — git-native PR view (thin keystone)

usage:
  nugit init [flags]          scaffold .nugit/ and bootstrap a C4 model from the import graph
  nugit pr-render [flags]      compute & render the unified PR view
  nugit version

init flags:
  -C dir         repo directory (default ".")
  -mode m        c4 enforcement written to config: warn (default) | enforce
  -no-model      scaffold only; write a template workspace.dsl instead of bootstrapping
  -force         overwrite existing .nugit files

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
	_ = fs.Parse(args)

	res, err := scaffold.Run(scaffold.Options{
		RepoDir: *dir, Force: *force, NoModel: *noModel, Mode: *mode,
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
	case *noModel:
		fmt.Println("Scaffolded .nugit/ with a template workspace.dsl. Define your components + paths globs, then run `nugit pr-render`.")
	case res.ModelEmpty:
		fmt.Println("No Go packages found — wrote a template workspace.dsl. Define your components and paths globs by hand.")
	case res.WroteModel:
		fmt.Printf("Bootstrapped a C4 model: %d component(s), %d dependency edge(s), in %s mode.\n",
			res.Components, res.Edges, res.Mode)
		fmt.Println("Next: review .nugit/architecture/workspace.dsl, then run `nugit pr-render`.")
		if res.Mode == "warn" {
			fmt.Println("When the model is ratified, set c4.mode to `enforce` in .nugit/config.yml.")
		}
	case res.Components > 0:
		fmt.Printf("A workspace.dsl already exists (left unchanged); discovered %d component(s), %d edge(s). Use -force to regenerate.\n",
			res.Components, res.Edges)
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
