// Package modelfacts assembles the deterministic ground truth the ADR-0012 bootstrap
// agent reasons over (and may not contradict): the deployable-container inventory
// (SPEC-003) plus the dependency-edge graph and the shared libraries. The agent turns
// this into a grouped, named workspace.dsl; nugit owns only the facts + the schema.
package modelfacts

import (
	"path/filepath"
	"strings"

	"github.com/n8o/nugit/internal/cmake"
	"github.com/n8o/nugit/internal/deploy"
)

// Bundle is the grounding handed to the bootstrap agent.
type Bundle struct {
	Repo        string             `json:"repo"`
	Containers  []deploy.Container `json:"containers"` // the inventory with tiers + deploy-confirmation
	Summary     deploy.Summary     `json:"summary"`
	Edges       [][2]string        `json:"dependency_edges"`  // [srcDir, dstDir] from the build graph
	Libs        []string           `json:"shared_libs"`       // libs/* dirs = candidate shared components
	Instruction string             `json:"agent_instruction"` // what the agent must (not) do
}

// Facts gathers the grounding for repoDir. Best-effort: the dependency graph is empty
// when the repo isn't CMake, but the container inventory always populates.
func Facts(repoDir string) (Bundle, error) {
	containers, sum, err := deploy.Detect(repoDir)
	if err != nil {
		return Bundle{}, err
	}
	g, _ := cmake.Discover(repoDir) // best-effort dependency edges
	var libs []string
	for _, d := range g.Dirs {
		if strings.HasPrefix(d, "libs/") || strings.Contains(d, "/libs/") {
			libs = append(libs, d)
		}
	}
	return Bundle{
		Repo:       filepath.Base(strings.TrimRight(repoDir, "/")),
		Containers: containers,
		Summary:    sum,
		Edges:      g.Edges,
		Libs:       libs,
		Instruction: "Ground truth, do not contradict: every C4 container must trace to a " +
			"detected container or a deployed-not-detected gap below; libs are components, " +
			"never containers; group containers into named sub-systems; resolve NEEDS-AGENT " +
			"using deploy-confirmation + the gaps; emit valid Structurizr DSL " +
			"(system → container → component, multi-line quoted properties); land as a warn-mode PR.",
	}, nil
}
