workspace "nugit" "Git-native typed knowledge & unified PR view — self-model (bootstrapping spike)" {

    model {
        nugit = softwareSystem "nugit" "The engine + CLI" {
          app = container "nugit" "The engine + CLI binary" "Go" {

            // Each component binds to physical files via properties { paths },
            // which is how the file->component mapping is computed (the review's
            // missing primitive). The relationships below are nugit's REAL
            // internal import graph — `nugit pr-render` validates code against it.

            model_ = component "Domain model" "Shared dependency-free types" "Go" {
                properties {
                    "paths" "internal/model/**"
                }
            }
            gitutil = component "Git plumbing" "Shells out to git" "Go" {
                properties {
                    "paths" "internal/gitutil/**"
                }
            }
            trailers = component "Trailer parser" "Parses commit-trailer convention" "Go" {
                properties {
                    "paths" "internal/trailers/**"
                }
            }
            goimports = component "Go import analyzer" "Maps imports to dirs via go/parser" "Go" {
                properties {
                    "paths" "internal/goimports/**"
                }
            }
            cmake = component "CMake analyzer" "Static target_link_libraries dependency graph" "Go" {
                properties {
                    "paths" "internal/cmake/**"
                }
            }
            pyimports = component "Python analyzer" "Python import graph (pure Go)" "Go" {
                properties {
                    "paths" "internal/pyimports/**"
                }
            }
            tsdeps = component "TS analyzer" "TypeScript graph via dependency-cruiser" "Go" {
                properties {
                    "paths" "internal/tsdeps/**"
                }
            }
            c4 = component "C4 parser + diff" "Structurizr DSL subset + structural delta" "Go" {
                properties {
                    "paths" "internal/c4/**"
                }
            }
            mapping = component "File->component mapper" "Glob resolution of paths to components" "Go" {
                properties {
                    "paths" "internal/mapping/**"
                }
            }
            knowledge = component "Knowledge reader" "Reads .nugit/** objects + supersede graph" "Go" {
                properties {
                    "paths" "internal/knowledge/**"
                }
            }
            localmem = component "Working memory" "Ephemeral .nugit-local per-agent notes" "Go" {
                properties {
                    "paths" "internal/localmem/**"
                }
            }
            delta = component "Delta engine" "Computes the four deterministic deltas" "Go" {
                properties {
                    "paths" "internal/delta/**"
                }
            }
            beads = component "Beads adapter" "Reads .beads/*.jsonl for plan position" "Go" {
                properties {
                    "paths" "internal/beads/**"
                }
            }
            icepanel = component "IcePanel projection" "Transforms the C4 model to IcePanel import data (outbound)" "Go" {
                properties {
                    "paths" "internal/icepanel/**"
                }
            }
            notion = component "Notion projection" "Publishes the knowledge corpus to a Notion database (outbound)" "Go" {
                properties {
                    "paths" "internal/notion/**"
                }
            }
            consistency = component "Consistency checks" "Cross-artifact verification" "Go" {
                properties {
                    "paths" "internal/consistency/**"
                }
            }
            significance = component "Significance classifier" "Heuristic disclosure tier" "Go" {
                properties {
                    "paths" "internal/significance/**"
                }
            }
            render = component "Renderer" "Markdown / check-run / structured JSON" "Go" {
                properties {
                    "paths" "internal/render/**"
                }
            }
            narrative = component "LLM narrative" "Opt-in cached prose summary" "Go" {
                properties {
                    "paths" "internal/narrative/**"
                }
            }
            engine = component "Engine" "Orchestrates the pr-render pipeline" "Go" {
                properties {
                    "paths" "internal/engine/**"
                }
            }
            config = component "Config" "Reads .nugit/config.yml" "Go" {
                properties {
                    "paths" "internal/config/**"
                }
            }
            bootstrap = component "Bootstrap" "Reverse-engineers a C4 model from the import graph" "Go" {
                properties {
                    "paths" "internal/bootstrap/**"
                }
            }
            scaffold = component "Scaffold" "nugit init: writes the .nugit/ tree" "Go" {
                properties {
                    "paths" "internal/scaffold/**"
                }
            }
            distill = component "Distiller" "Promotes commit trailers to durable knowledge" "Go" {
                properties {
                    "paths" "internal/distill/**"
                }
            }
            ratify = component "Ratifier" "Promotes proposed knowledge to the ratified corpus (ADR-0016)" "Go" {
                properties {
                    "paths" "internal/ratify/**"
                }
            }
            evidence = component "Evidence tiers" "Derives the per-object trust tier (enforced/checked/declared/proposed/stale)" "Go" {
                properties {
                    "paths" "internal/evidence/**"
                }
            }
            eval = component "Eval" "Labeled corpus measuring heuristic accuracy" "Go" {
                properties {
                    "paths" "internal/eval/**"
                }
            }
            retrieval = component "Retrieval" "context(path): scoped typed knowledge bundle" "Go" {
                properties {
                    "paths" "internal/retrieval/**"
                }
            }
            usage = component "Usage log" "Local gitignored context() call log + stats aggregation" "Go" {
                properties {
                    "paths" "internal/usage/**"
                }
            }
            mcp = component "MCP server" "Exposes context() as an MCP stdio tool" "Go" {
                properties {
                    "paths" "internal/mcp/**"
                }
            }
            doctor = component "Doctor" "Setup pre-flight health checks" "Go" {
                properties {
                    "paths" "internal/doctor/**"
                }
            }
            obsidian = component "Obsidian vault" "Generates the knowledge index for the .nugit Obsidian vault" "Go" {
                properties {
                    "paths" "internal/obsidian/**"
                }
            }
            deploy = component "Deployable detector" "Derives the container inventory from Dockerfiles + CMake install (SPEC-003)" "Go" {
                properties {
                    "paths" "internal/deploy/**"
                }
            }
            modelfacts = component "Model facts" "Grounding bundle for the ADR-0012 bootstrap agent (inventory + edges + libs)" "Go" {
                properties {
                    "paths" "internal/modelfacts/**"
                }
            }
            agentcfg = component "Agent wiring" "Per-client MCP config snippets + claude-code install (nugit agent)" "Go" {
                properties {
                    "paths" "internal/agentcfg/**"
                }
            }
            cli = component "CLI" "nugit command entrypoint" "Go" {
                properties {
                    "paths" "cmd/nugit/**"
                }
            }

            // --- real dependency edges (kept in sync with the import graph) ---
            gitutil -> model_ "uses types"
            trailers -> model_ "uses types"
            c4 -> model_ "uses types"
            mapping -> model_ "uses types"
            knowledge -> model_ "uses types"
            significance -> model_ "uses types"
            render -> model_ "uses types"
            render -> c4 "renders the focused C4 diff diagram"

            delta -> model_ "uses types"
            delta -> c4 "parses + diffs the model"
            delta -> gitutil "reads refs"
            delta -> knowledge "parses objects"
            delta -> mapping "groups by component"
            delta -> beads "reads the plan graph"

            beads -> model_ "uses types"
            beads -> gitutil "reads the store at the reviewed ref"

            consistency -> model_ "uses types"
            consistency -> gitutil "reads refs"
            consistency -> goimports "reads import graph"
            consistency -> knowledge "edge traversal"
            consistency -> mapping "resolves components"
            consistency -> trailers "validates capture hygiene"
            consistency -> cmake "re-derives the CMake graph for enforcement"
            consistency -> pyimports "re-derives the Python graph for enforcement"
            consistency -> tsdeps "re-derives the TS graph for enforcement"

            engine -> model_ "uses types"
            engine -> consistency "runs checks"
            engine -> delta "computes deltas"
            engine -> gitutil "merge-base"
            engine -> goimports "module path"
            engine -> knowledge "loads objects"
            engine -> mapping "builds mapper"
            engine -> significance "classifies"
            engine -> trailers "parses commits"
            engine -> config "loads config"
            engine -> narrative "optional prose"

            narrative -> model_ "uses types"

            bootstrap -> goimports "reads import graph"
            bootstrap -> cmake "reads CMake dependency graph"
            bootstrap -> pyimports "reads Python import graph"
            bootstrap -> tsdeps "reads TS dependency graph"
            bootstrap -> mapping "resolves edges like the check does"
            bootstrap -> gitutil "git-root prefix for globs"
            bootstrap -> model_ "uses types"

            scaffold -> bootstrap "generates the model"
            scaffold -> gitutil "installs the commit-msg hook"

            distill -> gitutil "reads the commit range"
            distill -> trailers "parses trailers"
            distill -> knowledge "dedups against the store"
            distill -> model_ "uses types"
            engine -> distill "computes the PR-time proposal set (ADR-0018)"

            eval -> engine "runs the corpus"
            eval -> model_ "uses types"

            retrieval -> c4 "parses the model"
            retrieval -> config "reads config"
            retrieval -> knowledge "loads objects"
            retrieval -> mapping "resolves the component"
            retrieval -> model_ "uses types"
            retrieval -> localmem "pulls ephemeral notes"

            mcp -> retrieval "serves context()"
            mcp -> usage "logs served bundles"

            usage -> retrieval "summarizes bundles"
            usage -> config "honors the usage.log knob"
            usage -> gitutil "stamps the current branch on records"

            cli -> model_ "uses types"
            cli -> engine "builds report"
            cli -> render "emits output"
            cli -> scaffold "runs init"
            cli -> config "reads fail-on default"
            cli -> retrieval "context command"
            cli -> mcp "mcp command"
            cli -> usage "logs context calls; stats command"
            cli -> distill "distill command"
            cli -> ratify "ratify command"
            ratify -> model_ "uses types"
            ratify -> knowledge "loads + resolves objects"
            evidence -> model_ "uses types"
            evidence -> knowledge "parses relates_to edges"
            evidence -> bootstrap "detects edge-check backends"
            evidence -> tsdeps "checks dependency-cruiser availability"
            consistency -> evidence "governed-components resolver"
            engine -> evidence "annotates trust tiers"
            retrieval -> evidence "annotates bundle items"
            cli -> localmem "remember command"
            cli -> trailers "commit-msg hook validation"
            cli -> c4 "c4 render command"
            cli -> consistency "explain command"
            cli -> doctor "doctor command"
            cli -> icepanel "c4 export command"
            cli -> notion "notion publish command"

            icepanel -> model_ "uses types"

            notion -> model_ "uses types"
            notion -> knowledge "loads the corpus"

            doctor -> config "checks config"
            doctor -> c4 "checks the model parses"
            doctor -> gitutil "checks the hook"
            doctor -> bootstrap "detects the backend"
            doctor -> knowledge "checks the store"
            doctor -> model_ "uses types"

            cli -> obsidian "obsidian command"
            cli -> deploy "deploy command"
            cli -> modelfacts "model facts command"
            cli -> agentcfg "agent command"

            modelfacts -> deploy "reads the container inventory"
            modelfacts -> cmake "reads the dependency edges"
            obsidian -> knowledge "loads the corpus"
            obsidian -> model_ "uses types"
          }
        }
    }

    views {
        component app "Components" {
            include *
            autolayout lr
        }
        theme default
    }
}
