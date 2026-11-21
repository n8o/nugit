package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()

	// HIGH-3: C++ service — all three signals agree (apps dir + install(RUNTIME) + Dockerfile binary).
	write(t, root, "apps/router_service/CMakeLists.txt",
		"add_executable(router_service main.cpp)\ninstall(TARGETS router_service RUNTIME DESTINATION bin)\n")
	write(t, root, "docker/Dockerfile.router_service",
		"FROM sdk AS builder\nRUN cmake --build .\nFROM runtime\nCOPY --from=builder build/apps/router_service/router_service /usr/local/bin/\n")

	// HIGH-2: Python service — Dockerfile ships apps/<svc>/src, CMake legitimately absent.
	write(t, root, "apps/ai_processor/requirements.txt", "torch\n")
	write(t, root, "docker/Dockerfile.ai_processor",
		"FROM python:3.12\nCOPY apps/ai_processor/src /app\nRUN pip install -r requirements.txt\n")

	// Bench false-positive: every container signal fires, but the name blocklist wins.
	write(t, root, "apps/sample_bench/CMakeLists.txt",
		"add_executable(sample_bench b.cpp)\ninstall(TARGETS sample_bench RUNTIME DESTINATION bin)\n")
	write(t, root, "docker/Dockerfile.sample_bench",
		"FROM runtime\nCOPY --from=builder build/apps/sample_bench/sample_bench /usr/local/bin/\n")

	// Base image: excluded.
	write(t, root, "docker/Dockerfile.base", "FROM ubuntu\nRUN apt-get update\n")

	// Dev-variant Dockerfile: excluded (non-production).
	write(t, root, "apps/router_service/Dockerfile.dev", "FROM cpp\nRUN make dev\n")

	// Pruned: a decoy Dockerfile in node_modules must never count.
	write(t, root, "apps/web/node_modules/pkg/Dockerfile", "FROM node\nCOPY . /app\n")

	// k8s manifest: deploy-confirms router-service (first-party); postgres is infra.
	write(t, root, "k8s/router.yaml",
		"kind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.acme.com/app/router-service:GOLDEN\n        - image: postgres:16-alpine\n")

	got, sum, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}

	by := map[string]Container{}
	for _, c := range got {
		by[c.Name] = c
	}
	if sum.Containers != 2 {
		t.Fatalf("want 2 containers (router-service, ai-processor), got %d: %+v", sum.Containers, got)
	}
	if c := by["router-service"]; c.Confidence != "HIGH-3" || c.Language != "cpp" || c.Binary != "router_service" {
		t.Errorf("router-service = %+v, want HIGH-3 cpp binary=router_service", c)
	}
	if c := by["ai-processor"]; c.Confidence != "HIGH-2" || c.Language != "python" {
		t.Errorf("ai-processor = %+v, want HIGH-2 python", c)
	}
	if _, ok := by["sample-bench"]; ok {
		t.Error("sample_bench is a bench — must be excluded despite every positive signal")
	}
	if _, ok := by["base"]; ok {
		t.Error("base image must be excluded")
	}
	if _, ok := by["dev"]; ok {
		t.Error("Dockerfile.dev variant must be excluded")
	}
	if sum.High3 != 1 || sum.High2 != 1 {
		t.Errorf("tiers = %+v, want High3=1 High2=1", sum)
	}
	// DETECT-3 gate.
	if sum.FirstPartyRegistry != "registry.acme.com" {
		t.Errorf("first-party registry = %q, want registry.acme.com", sum.FirstPartyRegistry)
	}
	if !by["router-service"].DeployConfirmed || sum.DeployConfirmed != 1 {
		t.Errorf("router-service should be deploy-confirmed (sum=%d): %+v", sum.DeployConfirmed, by["router-service"])
	}
	if sum.InfraImages != 1 {
		t.Errorf("infra images = %d, want 1 (postgres)", sum.InfraImages)
	}
}

func TestPruneAndInstallTargets(t *testing.T) {
	root := t.TempDir()
	write(t, root, "apps/foo/CMakeLists.txt", "install(TARGETS foo RUNTIME DESTINATION bin)\n")
	// install() under a pruned tree must NOT be picked up.
	write(t, root, "third_party/dep/apps/bar/CMakeLists.txt", "install(TARGETS bar RUNTIME DESTINATION bin)\n")

	m := InstallRuntimeTargets(root)
	if m["apps/foo"] != "foo" {
		t.Errorf("apps/foo install target = %q, want foo", m["apps/foo"])
	}
	if _, ok := m["third_party/dep/apps/bar"]; ok {
		t.Error("install() under third_party must be pruned")
	}
}

// A testing image is not a service, whatever it ships. An adapter named after
// the standard it bridges is a service: the standard's name is not a skip word.
func TestTestingImagesAreSkippedAndAdaptersCounted(t *testing.T) {
	root := t.TempDir()

	// Every container signal fires, and the name still marks it as a test rig.
	write(t, root, "apps/conformance/requirements.txt", "pytest\n")
	write(t, root, "docker/Dockerfile.conformance-testing",
		"FROM python:3.12\nCOPY apps/conformance/src /app\nRUN pip install -r requirements.txt\n")

	// A first-party service that bridges a protocol.
	write(t, root, "apps/mqtt_bridge_adapter/requirements.txt", "paho-mqtt\n")
	write(t, root, "docker/Dockerfile.mqtt-bridge-adapter",
		"FROM python:3.12\nCOPY apps/mqtt_bridge_adapter/src /app\nRUN pip install -r requirements.txt\n")

	// Both are deployed from the first-party registry.
	write(t, root, "k8s/apps.yaml",
		"kind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n"+
			"        - image: registry.acme.com/app/mqtt-bridge-adapter:GOLDEN\n"+
			"        - image: registry.acme.com/app/conformance-testing:GOLDEN\n")

	got, sum, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Container{}
	for _, c := range got {
		by[c.Name] = c
	}
	if _, ok := by["conformance-testing"]; ok || sum.BaseOrTest != 1 {
		t.Errorf("Dockerfile.conformance-testing must be skipped as a test image (base/test=%d): %+v", sum.BaseOrTest, got)
	}
	c, ok := by["mqtt-bridge-adapter"]
	if !ok || c.Confidence != "HIGH-2" || !c.DeployConfirmed || sum.Containers != 1 {
		t.Errorf("mqtt-bridge-adapter must be counted as a deploy-confirmed HIGH-2 service (containers=%d): %+v", sum.Containers, got)
	}
	for _, img := range sum.DeployedNotDetected {
		t.Errorf("a deployed testing image is not a gap in the model: %s", img)
	}

	for img, want := range map[string]bool{
		"testing": true, "conformance-testing": true, "testing-tools": true, "load_testing": true,
		"mqtt-bridge-adapter": false, "attestation": false, "contesting": false,
	} {
		if got := baseOrTestImage.MatchString(img); got != want {
			t.Errorf("baseOrTestImage(%q) = %v, want %v", img, got, want)
		}
	}
}

// A deployable the apps/ rules cannot place still gets a source dir when its
// build context or its COPY lines name one, and that never raises its tier or
// confirms it against a deployment by a dir name alone.
func TestDerivedSourceDirs(t *testing.T) {
	root := t.TempDir()

	// One source dir, named only by the files the image copies from it.
	write(t, root, "third_party/acme-bridge/adapter/package.json", "{}\n")
	write(t, root, "third_party/acme-bridge/adapter/index.js", "\n")
	write(t, root, "docker/Dockerfile.acme-bridge-adapter",
		"FROM node:22\nWORKDIR /app\nCOPY third_party/acme-bridge/adapter/package.json ./\n"+
			"COPY third_party/acme-bridge/adapter/index.js ./\nCMD [\"node\", \"index.js\"]\n")

	// A Dockerfile in its own source tree: the build context is its dir.
	write(t, root, "services/acme-ingest/main.py", "\n")
	write(t, root, "services/acme-ingest/Dockerfile", "FROM python:3.12\nCOPY . /app\n")

	// Two dirs; the one named for the image is its source. It ships Python,
	// which would be HIGH-2 under the apps/ rule, and must stay NEEDS-AGENT.
	write(t, root, "libs/acme_shared/shared.py", "\n")
	write(t, root, "tools/acme-report/requirements.txt", "requests\n")
	write(t, root, "docker/Dockerfile.acme-report",
		"FROM python:3.12\nCOPY libs/acme_shared/ /opt/shared/\nCOPY --chown=app:app tools/acme-report/ /opt/report/\n")

	// The exec (JSON) form, and a stage copy that is not the context.
	write(t, root, "tools/acme-json/run.sh", "#!/bin/sh\n")
	write(t, root, "docker/Dockerfile.acme-json",
		"FROM alpine\nCOPY --from=builder /out/acme /usr/local/bin/\nCOPY [\"tools/acme-json/run.sh\", \"/usr/local/bin/\"]\n")

	// Several unrelated dirs and a root file: no single source.
	write(t, root, "cmake/x.cmake", "\n")
	write(t, root, "common/c.h", "\n")
	write(t, root, "CMakeLists.txt", "project(acme)\n")
	write(t, root, "docker/Dockerfile.acme-suite",
		"FROM ubuntu\nCOPY cmake/ /src/cmake/\nCOPY libs/ /src/libs/\nCOPY common/ /src/common/\nCOPY CMakeLists.txt /src/\n")

	// Nothing copied at all.
	write(t, root, "docker/Dockerfile.acme-probe", "FROM alpine\nRUN apk add curl\n")

	// A deployment named for a derived dir's base, not for the image.
	write(t, root, "k8s/acme.yaml",
		"kind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.acme.com/app/adapter:1\n")

	got, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Container{}
	for _, c := range got {
		by[c.Name] = c
	}
	for name, want := range map[string]string{
		"acme-bridge-adapter": "third_party/acme-bridge/adapter",
		"acme-ingest":         "services/acme-ingest",
		"acme-report":         "tools/acme-report",
		"acme-json":           "tools/acme-json",
		"acme-suite":          "",
		"acme-probe":          "",
	} {
		c, ok := by[name]
		if !ok {
			t.Errorf("%s: not detected: %+v", name, got)
			continue
		}
		if c.SourceDir != want {
			t.Errorf("%s: source dir %q, want %q (evidence %v)", name, c.SourceDir, want, c.Evidence)
		}
		if c.Confidence != "NEEDS-AGENT" {
			t.Errorf("%s: a derived source dir must not raise the tier, got %s", name, c.Confidence)
		}
		if want != "" && !strings.Contains(strings.Join(c.Evidence, "; "), "source dir") {
			t.Errorf("%s: evidence must say where the source dir came from: %v", name, c.Evidence)
		}
	}
	if c := by["acme-report"]; c.Language != "python" {
		t.Errorf("acme-report: language from its derived dir = %q, want python", c.Language)
	}
	if by["acme-bridge-adapter"].DeployConfirmed {
		t.Error("a derived dir's base name must not confirm a deployment")
	}
}
