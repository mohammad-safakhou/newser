package runtime

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/config"
)

func writePolicy(t *testing.T, dir, provider string, network bool) string {
	t.Helper()
	path := filepath.Join(dir, "policy.yaml")
	contents := []byte("sandbox:\n  provider: " + provider + "\n  cpu: 1\n  memory: 512Mi\n  timeout: 60s\n  network:\n    enabled: " + boolToString(network) + "\n    allowlist: []\n")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}

func boolToString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestEnsureSandboxReportsStatus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policyPath := writePolicy(t, dir, "docker", false)
	cfg := &config.Config{Security: config.SecurityConfig{PolicyFile: policyPath, SandboxProvider: "docker", DefaultCPU: 1, DefaultMemory: "512Mi", DefaultTimeout: 60 * time.Second}}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	enforcer, normalized, err := EnsureSandbox(context.Background(), cfg, "worker", logger, SandboxRequest{})
	if err != nil {
		t.Fatalf("EnsureSandbox error: %v", err)
	}
	if enforcer == nil {
		t.Fatal("expected enforcer")
	}
	if normalized.Provider != "docker" {
		t.Fatalf("expected provider to be defaulted to docker, got %q", normalized.Provider)
	}
	if normalized.CPU != 1 {
		t.Fatalf("expected cpu to be defaulted to 1, got %v", normalized.CPU)
	}
	if got := buf.String(); got == "" {
		t.Fatal("expected log output, got empty string")
	} else {
		if !bytes.Contains(buf.Bytes(), []byte("sandbox=true")) {
			t.Fatalf("expected sandbox=true in log, got %q", got)
		}
		if !bytes.Contains(buf.Bytes(), []byte("provider=docker")) {
			t.Fatalf("expected provider in log, got %q", got)
		}
	}
}

func TestEnsureSandboxViolatesPolicy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policyPath := writePolicy(t, dir, "docker", false)
	cfg := &config.Config{Security: config.SecurityConfig{PolicyFile: policyPath, SandboxProvider: "docker", DefaultCPU: 1, DefaultMemory: "512Mi", DefaultTimeout: 60 * time.Second}}

	_, _, err := EnsureSandbox(context.Background(), cfg, "crawler", nil, SandboxRequest{NetworkEnabled: true})
	if err == nil {
		t.Fatal("expected error when requesting network access")
	}
}

func TestEnsureSandboxMissingProvider(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Write policy without provider to ensure config fallback is required.
	path := filepath.Join(dir, "policy.yaml")
	contents := []byte("sandbox:\n  cpu: 1\n  memory: 512Mi\n  timeout: 60s\n  network:\n    enabled: false\n    allowlist: []\n")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	cfg := &config.Config{Security: config.SecurityConfig{PolicyFile: path, SandboxProvider: "", DefaultCPU: 1, DefaultMemory: "512Mi", DefaultTimeout: 60 * time.Second}}

	if _, _, err := EnsureSandbox(context.Background(), cfg, "api", nil, SandboxRequest{}); err == nil {
		t.Fatal("expected error due to missing sandbox provider")
	}
}

func TestValidateMutatesRequest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policyPath := writePolicy(t, dir, "docker", false)
	cfg := &config.Config{Security: config.SecurityConfig{PolicyFile: policyPath, SandboxProvider: "docker", DefaultCPU: 2, DefaultMemory: "1Gi", DefaultTimeout: 120 * time.Second}}

	policy, err := LoadSandboxPolicy(cfg)
	if err != nil {
		t.Fatalf("LoadSandboxPolicy error: %v", err)
	}

	enforcer := NewSandboxEnforcer(policy)
	req := SandboxRequest{}
	if err := enforcer.Validate(context.Background(), &req); err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	if req.Provider != "docker" {
		t.Fatalf("expected provider docker, got %q", req.Provider)
	}
	if req.CPU != 2 {
		t.Fatalf("expected cpu 2, got %v", req.CPU)
	}
	if req.Timeout != 120*time.Second {
		t.Fatalf("expected timeout 120s, got %s", req.Timeout)
	}
}
