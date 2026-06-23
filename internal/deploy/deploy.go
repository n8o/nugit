// Package deploy is nugit's deterministic deployable-detector (SPEC-003): it derives
// a repo's container inventory from deploy artifacts — Dockerfiles (the spine)
// cross-checked with CMake install(TARGETS … RUNTIME) — and labels each container
// with a confidence tier and the source-line evidence the grounded-agent bootstrap
// (ADR-0012) may not contradict. The method is general; accuracy is repo-specific.
package deploy

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// pruneDirs hold no first-party deployables and inflate raw counts 7–20× (SPEC-003 §1).
var pruneDirs = map[string]bool{
	".git": true, ".nugit": true, ".worktrees": true, ".claude": true,
	"vendor": true, "third_party": true, "node_modules": true,
	"build": true, "target": true, "dist": true, "testdata": true,
	"cmake-build-debug": true, "out": true,
}

// baseOrTestImage matches Dockerfile suffixes that are NOT first-party services.
var baseOrTestImage = regexp.MustCompile(`(?i)(^|[.\-_])(base|builder|build|sdk|test|smoketest|init|installer|runtime|nmos|cni|pause|busybox|models)([.\-_]|$)`)

// benchName flags benchmark/test targets that pass every container signal but aren't services.
var benchName = regexp.MustCompile(`(?i)(^|[_\-])(bench|test|unit)([_\-]|$)`)

// devVariant images are non-production build variants (Dockerfile.dev), not services.
var devVariant = map[string]bool{"dev": true, "local": true, "debug": true}

var (
	copyFromBin = regexp.MustCompile(`(?im)^\s*COPY\s+--from=\S+\s+\S*?/([A-Za-z0-9_.\-]+)\s+(?:/usr/local/bin|/usr/bin|/app)`)
	cpBuildBin  = regexp.MustCompile(`build/apps/([A-Za-z0-9_.\-]+)/([A-Za-z0-9_.\-]+)`)
	copyAppsSrc = regexp.MustCompile(`(?im)^\s*COPY\s+(?:--from=\S+\s+)?(apps/[A-Za-z0-9_.\-]+)`)
	installRT   = regexp.MustCompile(`(?is)install\s*\(\s*TARGETS\s+([A-Za-z0-9_.\-]+)[^)]*?\bRUNTIME\b`)
)

// Dockerfile is a parsed deployable image definition.
type Dockerfile struct {
	Path         string // repo-relative
	Image        string // suffix of Dockerfile.<image>, else parent dir name
	Binary       string // artifact copied into the runtime stage, if any
	SourceDir    string // apps/<svc> the image ships, if determinable
	Language     string // cpp|python|rust|go|node|unknown
	IsBaseOrTest bool   // base/builder/sdk/test/init → not a first-party service
}

// Container is a detected deployable unit with confidence + evidence (SPEC-003 §7).
type Container struct {
	Name            string   `json:"name"`
	SourceDir       string   `json:"source_dir,omitempty"`
	Language        string   `json:"language"`
	Dockerfile      string   `json:"dockerfile"`
	Binary          string   `json:"binary,omitempty"`
	Confidence      string   `json:"confidence"` // HIGH-3 | HIGH-2 | NEEDS-AGENT
	DeployConfirmed bool     `json:"deploy_confirmed,omitempty"`
	Evidence        []string `json:"evidence"`
}

// Summary counts the inventory by tier.
type Summary struct {
	Dockerfiles         int      `json:"dockerfiles_scanned"`
	BaseOrTest          int      `json:"excluded_base_or_test"`
	Containers          int      `json:"containers"`
	High3               int      `json:"high_3_signal"`
	High2               int      `json:"high_2_signal"`
	NeedsAgent          int      `json:"needs_agent"`
	FirstPartyRegistry  string   `json:"first_party_registry,omitempty"`
	DeployConfirmed     int      `json:"deploy_confirmed"`
	InfraImages         int      `json:"infra_images_excluded"`
	DeployedNotDetected []string `json:"deployed_not_detected,omitempty"`
}

// Detect returns the container inventory for repoDir (SPEC-003).
func Detect(repoDir string) ([]Container, Summary, error) {
	dfs, err := ScanDockerfiles(repoDir)
	if err != nil {
		return nil, Summary{}, err
	}
	install := InstallRuntimeTargets(repoDir) // dir(rel) -> installed runtime target
	gate := ScanK8s(repoDir)                  // DETECT-3: deploy-confirmation + first-party gate
	sum := Summary{Dockerfiles: len(dfs), FirstPartyRegistry: gate.Registry, InfraImages: gate.InfraImages}

	var out []Container
	matched := map[string]bool{} // first-party gate keys claimed by a container
	for _, d := range dfs {
		if d.IsBaseOrTest {
			sum.BaseOrTest++
			continue
		}
		if benchName.MatchString(d.Image) || devVariant[d.Image] {
			continue // §3: bench/test/dev-variant images are not first-party services
		}
		c := Container{
			Name:       normalize(d.Image),
			SourceDir:  d.SourceDir,
			Language:   d.Language,
			Dockerfile: d.Path,
			Binary:     d.Binary,
			Evidence:   []string{"dockerfile: " + d.Path},
		}
		// §4/§7: triple-signal agreement when CMake install(RUNTIME) confirms the binary.
		if tgt, ok := install[d.SourceDir]; ok && (tgt == d.Binary || d.Binary == "") {
			c.Confidence = "HIGH-3"
			c.Language = "cpp"
			if c.Binary == "" {
				c.Binary = tgt
			}
			c.Evidence = append(c.Evidence, "install(TARGETS "+tgt+" … RUNTIME) in "+d.SourceDir+"/CMakeLists.txt")
		} else if d.SourceDir != "" && d.Language != "cpp" && d.Language != "unknown" {
			c.Confidence = "HIGH-2" // §5: CMake legitimately absent for non-C++
			c.Evidence = append(c.Evidence, "ships "+d.SourceDir+" ("+d.Language+", no install(RUNTIME) by design)")
		} else {
			c.Confidence = "NEEDS-AGENT"
			c.Evidence = append(c.Evidence, "no triple-signal agreement — source/binary correlation unresolved")
		}
		// §8: a nested Dockerfile (not under docker/, has sibling Dockerfiles) is a
		// multi-deployable subsystem the agent must name/expand.
		if isNested(d.Path) {
			c.Confidence = "NEEDS-AGENT"
			c.Evidence = append(c.Evidence, "nested Dockerfile in a multi-deployable subsystem")
		}
		// DETECT-3: confirm against a real first-party k8s workload image.
		if img, key := gate.match(c); img != "" {
			c.DeployConfirmed = true
			c.Evidence = append(c.Evidence, "deploy-confirmed: "+img)
			matched[key] = true
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for _, c := range out {
		sum.Containers++
		if c.DeployConfirmed {
			sum.DeployConfirmed++
		}
		switch c.Confidence {
		case "HIGH-3":
			sum.High3++
		case "HIGH-2":
			sum.High2++
		default:
			sum.NeedsAgent++
		}
	}
	// Gaps: first-party images deployed but matched by no detected container — the
	// agent's "deployed, not yet modelled" queue (base/init images filtered out).
	for k, img := range gate.FirstParty {
		if !matched[k] && !baseOrTestImage.MatchString(k) {
			sum.DeployedNotDetected = append(sum.DeployedNotDetected, img)
		}
	}
	sort.Strings(sum.DeployedNotDetected)
	return out, sum, nil
}

// ScanDockerfiles finds and parses every (pruned) Dockerfile in repoDir.
func ScanDockerfiles(repoDir string) ([]Dockerfile, error) {
	var out []Dockerfile
	err := filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != repoDir && pruneDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "Dockerfile" && !strings.HasPrefix(name, "Dockerfile.") {
			return nil
		}
		if strings.HasSuffix(name, ".dockerignore") {
			return nil
		}
		rel, _ := filepath.Rel(repoDir, p)
		out = append(out, parseDockerfile(repoDir, filepath.ToSlash(rel)))
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func parseDockerfile(repoDir, rel string) Dockerfile {
	df := Dockerfile{Path: rel}
	base := filepath.Base(rel)
	if strings.HasPrefix(base, "Dockerfile.") {
		df.Image = strings.TrimPrefix(base, "Dockerfile.")
	} else {
		df.Image = filepath.Base(filepath.Dir(rel)) // a bare Dockerfile takes its dir name
	}
	df.IsBaseOrTest = baseOrTestImage.MatchString(df.Image)

	body, _ := os.ReadFile(filepath.Join(repoDir, rel))
	text := string(body)
	if m := copyFromBin.FindStringSubmatch(text); m != nil {
		df.Binary = m[1]
	}
	if m := cpBuildBin.FindStringSubmatch(text); m != nil {
		df.SourceDir, df.Binary = "apps/"+m[1], m[2]
	}
	if df.SourceDir == "" {
		if m := copyAppsSrc.FindStringSubmatch(text); m != nil {
			df.SourceDir = m[1]
		}
	}
	df.Language = detectLanguage(repoDir, df.SourceDir, text)
	return df
}

// detectLanguage prefers the source dir's build files, falling back to Dockerfile cues.
func detectLanguage(repoDir, srcDir, dockerText string) string {
	if srcDir != "" {
		d := filepath.Join(repoDir, srcDir)
		switch {
		case exists(d, "CMakeLists.txt"):
			return "cpp"
		case exists(d, "Cargo.toml"):
			return "rust"
		case exists(d, "go.mod"):
			return "go"
		case exists(d, "requirements.txt") || exists(d, "pyproject.toml") || exists(d, "setup.py"):
			return "python"
		case exists(d, "package.json"):
			return "node"
		}
	}
	switch {
	case strings.Contains(dockerText, "cargo "):
		return "rust"
	case regexp.MustCompile(`(?i)pip\s+install|python`).MatchString(dockerText):
		return "python"
	case strings.Contains(dockerText, "go build"):
		return "go"
	case regexp.MustCompile(`(?i)cmake|make\b`).MatchString(dockerText):
		return "cpp"
	}
	return "unknown"
}

// InstallRuntimeTargets maps each apps/* dir to its sole install(TARGETS … RUNTIME)
// target (SPEC-003 §4): the C++ container confirmer, restricted to apps/.
func InstallRuntimeTargets(repoDir string) map[string]string {
	out := map[string]string{}
	appsRoot := filepath.Join(repoDir, "apps")
	_ = filepath.WalkDir(appsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if pruneDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "CMakeLists.txt" {
			return nil
		}
		body, _ := os.ReadFile(p)
		if m := installRT.FindStringSubmatch(string(body)); m != nil {
			rel, _ := filepath.Rel(repoDir, filepath.Dir(p))
			out[filepath.ToSlash(rel)] = m[1]
		}
		return nil
	})
	return out
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// isNested reports a Dockerfile that lives inside a source subtree (a multi-deployable
// subsystem) rather than the top-level docker/ image directory.
func isNested(rel string) bool {
	return !strings.HasPrefix(rel, "docker/") && strings.Contains(rel, "/")
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
