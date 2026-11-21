package deploy

import (
	"os"
	"path/filepath"
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
