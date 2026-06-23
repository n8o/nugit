package deploy

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SPEC-003 §8: k8s/helm CONFIRMS which containers are deployed and supplies the
// first-party image gate; it never BOUNDS the set (most pods are operator/CRD-created
// at runtime). So this is an additive signal — deploy-confirmation and gap-finding —
// not a source of truth for the inventory.

var imageRef = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*["']?([A-Za-z0-9][^"'\s]*)["']?`)

// publicRegistries are never a repo's own first-party registry.
var publicRegistries = map[string]bool{
	"docker.io": true, "index.docker.io": true, "ghcr.io": true, "gcr.io": true,
	"k8s.gcr.io": true, "registry.k8s.io": true, "quay.io": true, "nvcr.io": true,
	"mcr.microsoft.com": true, "public.ecr.aws": true, "registry.gitlab.com": true,
	"docker.elastic.co": true,
}

// K8sGate is the deploy-confirmation signal: the first-party image catalogue
// discovered in k8s/helm manifests, keyed by normalized image basename.
type K8sGate struct {
	Registry    string            // auto-detected first-party registry ("" if none)
	FirstParty  map[string]string // basename(normalized) -> full image ref
	InfraImages int               // distinct third-party images (infra, excluded)
}

// ScanK8s collects image refs from k8s/helm manifests and classifies first-party
// (the dominant non-public registry) vs infra.
func ScanK8s(repoDir string) K8sGate {
	images := map[string]bool{}
	for _, sub := range []string{"k8s", "helm", "deploy", "deployments", "manifests", "charts"} {
		root := filepath.Join(repoDir, sub)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if pruneDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if n := d.Name(); !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
				return nil
			}
			body, _ := os.ReadFile(p)
			for _, m := range imageRef.FindAllStringSubmatch(string(body), -1) {
				img := m[1]
				if strings.Contains(img, "{{") || strings.Contains(img, "$(") || strings.Contains(img, "${") {
					continue // unresolved helm/template placeholder
				}
				images[img] = true
			}
			return nil
		})
	}

	// First-party registry = the dominant non-public registry by distinct images.
	regCount := map[string]int{}
	for img := range images {
		if r := registryOf(img); r != "" && !publicRegistries[r] {
			regCount[r]++
		}
	}
	registry, best := "", 0
	for r, n := range regCount {
		if n > best {
			registry, best = r, n
		}
	}

	gate := K8sGate{Registry: registry, FirstParty: map[string]string{}}
	for img := range images {
		if registry != "" && registryOf(img) == registry {
			gate.FirstParty[normalize(basenameOf(img))] = img
		} else {
			gate.InfraImages++
		}
	}
	return gate
}

// match reports the first-party image (and the gate key) a container is deployed as,
// tolerating the common name skews (bare name, source-dir basename, ±"-service").
func (g K8sGate) match(c Container) (image, key string) {
	for _, k := range confirmKeys(c) {
		if img, ok := g.FirstParty[k]; ok {
			return img, k
		}
	}
	return "", ""
}

func confirmKeys(c Container) []string {
	ks := []string{c.Name, c.Name + "-service", strings.TrimSuffix(c.Name, "-service")}
	if c.SourceDir != "" {
		ks = append(ks, normalize(filepath.Base(c.SourceDir)))
	}
	return ks
}

// registryOf returns the registry host of an image ref, or "" for docker.io images
// (a bare name or a docker-hub user image, neither of which is a private registry).
func registryOf(img string) string {
	if i := strings.Index(img, "@"); i >= 0 {
		img = img[:i]
	}
	first, _, ok := strings.Cut(img, "/")
	if !ok {
		return "" // "busybox:1.36" → docker.io library
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first
	}
	return "" // "moby/buildkit" → docker.io user image
}

// basenameOf returns the final repo segment of an image ref, without registry or tag.
func basenameOf(img string) string {
	if i := strings.Index(img, "@"); i >= 0 {
		img = img[:i]
	}
	slash := strings.LastIndex(img, "/")
	if colon := strings.LastIndex(img, ":"); colon > slash {
		img = img[:colon] // strip tag (a ':' after the last '/' is a tag, not a port)
	}
	if slash := strings.LastIndex(img, "/"); slash >= 0 {
		img = img[slash+1:]
	}
	return img
}
