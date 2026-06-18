workspace "nugit" "Git-native typed knowledge & unified PR view — self-model (bootstrapping spike)" {

    model {
        nugit = softwareSystem "nugit" "The engine + CLI" {

            // Each component binds to physical files via properties { paths },
            // which is how the file->component mapping is computed (the review's
            // missing primitive). The relationships below are nugit's REAL
            // internal import graph — `nugit pr-render` validates code against it.

            model_ = component "Domain model" "Shared dependency-free types" "Go" {
                properties { paths "internal/model/**" }
            }
            gitutil = component "Git plumbing" "Shells out to git" "Go" {
                properties { paths "internal/gitutil/**" }
            }
            trailers = component "Trailer parser" "Parses commit-trailer convention" "Go" {
                properties { paths "internal/trailers/**" }
            }
            goimports = component "Go import analyzer" "Maps imports to dirs via go/parser" "Go" {
                properties { paths "internal/goimports/**" }
            }
            c4 = component "C4 parser + diff" "Structurizr DSL subset + structural delta" "Go" {
                properties { paths "internal/c4/**" }
            }
            mapping = component "File->component mapper" "Glob resolution of paths to components" "Go" {
                properties { paths "internal/mapping/**" }
            }
            knowledge = component "Knowledge reader" "Reads .nugit/** objects + supersede graph" "Go" {
                properties { paths "internal/knowledge/**" }
            }
            delta = component "Delta engine" "Computes the four deterministic deltas" "Go" {
                properties { paths "internal/delta/**" }
            }
            consistency = component "Consistency checks" "Cross-artifact verification" "Go" {
                properties { paths "internal/consistency/**" }
            }
            significance = component "Significance classifier" "Heuristic disclosure tier" "Go" {
                properties { paths "internal/significance/**" }
            }
            render = component "Renderer" "Markdown / check-run / structured JSON" "Go" {
                properties { paths "internal/render/**" }
            }
            engine = component "Engine" "Orchestrates the pr-render pipeline" "Go" {
                properties { paths "internal/engine/**" }
            }
            config = component "Config" "Reads .nugit/config.yml" "Go" {
                properties { paths "internal/config/**" }
            }
            bootstrap = component "Bootstrap" "Reverse-engineers a C4 model from the import graph" "Go" {
                properties { paths "internal/bootstrap/**" }
            }
            scaffold = component "Scaffold" "nugit init: writes the .nugit/ tree" "Go" {
                properties { paths "internal/scaffold/**" }
            }
            cli = component "CLI" "nugit command entrypoint" "Go" {
                properties { paths "cmd/nugit/**" }
            }

            // --- real dependency edges (kept in sync with the import graph) ---
            gitutil -> model_ "uses types"
            trailers -> model_ "uses types"
            c4 -> model_ "uses types"
            mapping -> model_ "uses types"
            knowledge -> model_ "uses types"
            significance -> model_ "uses types"
            render -> model_ "uses types"

            delta -> model_ "uses types"
            delta -> c4 "parses + diffs the model"
            delta -> gitutil "reads refs"
            delta -> knowledge "parses objects"
            delta -> mapping "groups by component"

            consistency -> model_ "uses types"
            consistency -> gitutil "reads refs"
            consistency -> goimports "reads import graph"
            consistency -> knowledge "edge traversal"
            consistency -> mapping "resolves components"
            consistency -> trailers "validates capture hygiene"

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

            bootstrap -> goimports "reads import graph"

            scaffold -> bootstrap "generates the model"

            cli -> model_ "uses types"
            cli -> engine "builds report"
            cli -> render "emits output"
            cli -> scaffold "runs init"
        }
    }

    views {
        component nugit "Components" {
            include *
            autolayout lr
        }
        theme default
    }
}
