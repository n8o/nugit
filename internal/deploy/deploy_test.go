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
	write(t, root, "apps/mxl_bridge_bench/CMakeLists.txt",
		"add_executable(mxl_bridge_bench b.cpp)\ninstall(TARGETS mxl_bridge_bench RUNTIME DESTINATION bin)\n")
	write(t, root, "docker/Dockerfile.mxl_bridge_bench",
		"FROM runtime\nCOPY --from=builder build/apps/mxl_bridge_bench/mxl_bridge_bench /usr/local/bin/\n")

	// Base image: excluded.
	write(t, root, "docker/Dockerfile.base", "FROM ubuntu\nRUN apt-get update\n")

	// Pruned: a decoy Dockerfile in node_modules must never count.
	write(t, root, "apps/web/node_modules/pkg/Dockerfile", "FROM node\nCOPY . /app\n")

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
	if _, ok := by["mxl-bridge-bench"]; ok {
		t.Error("mxl_bridge_bench is a bench — must be excluded despite every positive signal")
	}
	if _, ok := by["base"]; ok {
		t.Error("base image must be excluded")
	}
	if sum.High3 != 1 || sum.High2 != 1 {
		t.Errorf("tiers = %+v, want High3=1 High2=1", sum)
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
