// Command nugit is the thin-keystone CLI from the re-shaped plan: it renders the
// unified PR view from a repo's own committed artifacts. No index, no
// content-addressing, no embeddings, no merge driver — those are deferred until
// the keystone proves pull (see PLAN.md).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/burrowfarm/nugit/internal/engine"
	"github.com/burrowfarm/nugit/internal/model"
	"github.com/burrowfarm/nugit/internal/render"
)

const usage = `nugit — git-native PR view (thin keystone)

usage:
  nugit pr-render [flags]      compute & render the unified PR view
  nugit version

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

func cmdPRRender(args []string) int {
	fs := flag.NewFlagSet("pr-render", flag.ExitOnError)
	dir := fs.String("C", ".", "repo directory")
	base := fs.String("base", "HEAD~1", "base ref / target branch")
	head := fs.String("head", "HEAD", "head ref")
	format := fs.String("format", "markdown", "output format: markdown|check-run|json")
	failOn := fs.String("fail-on", "fail", "exit non-zero at severity: fail|warn|none")
	_ = fs.Parse(args)

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
