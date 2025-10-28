package policy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/newser/config"
)

func TestLoadSecurityPolicyAppliesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	payload := `
sandbox:
  image_allowlist:
    - alpine:3.20
  max_cpu: 4
  max_memory: 2Gi
  max_timeout: 5m
  network:
    enabled: true
    allowlist:
      - https://example.com
    denylist:
      - http://bad.com
`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	cfg := &config.Config{Security: config.SecurityConfig{
		PolicyFile:      path,
		SandboxProvider: "docker",
		DefaultCPU:      2,
		DefaultMemory:   "512Mi",
		DefaultTimeout:  90 * time.Second,
	}}

	policy, err := LoadSecurityPolicy(cfg)
	if err != nil {
		t.Fatalf("LoadSecurityPolicy: %v", err)
	}

	if policy.Sandbox.Provider != "docker" {
		t.Fatalf("expected provider fallback, got %q", policy.Sandbox.Provider)
	}
	if policy.Sandbox.CPU != 2 {
		t.Fatalf("expected cpu defaulted to 2, got %v", policy.Sandbox.CPU)
	}
	if policy.Sandbox.Memory != "512Mi" {
		t.Fatalf("expected memory defaulted to 512Mi, got %s", policy.Sandbox.Memory)
	}
	if policy.Sandbox.Timeout != "1m30s" {
		t.Fatalf("expected timeout default 1m30s, got %s", policy.Sandbox.Timeout)
	}
	if len(policy.Sandbox.Network.Allowlist) != 1 || policy.Sandbox.Network.Allowlist[0] != "example.com" {
		t.Fatalf("expected allowlist normalized host, got %+v", policy.Sandbox.Network.Allowlist)
	}
	if len(policy.Sandbox.Network.Denylist) != 1 || policy.Sandbox.Network.Denylist[0] != "bad.com" {
		t.Fatalf("expected denylist normalized host, got %+v", policy.Sandbox.Network.Denylist)
	}
}

func TestLoadSecurityPolicyValidationError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	payload := `
sandbox:
  provider: docker
  cpu: 4
  max_cpu: 2
  memory: 1Gi
  max_memory: 512Mi
  timeout: 60s
  max_timeout: 30s
`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	cfg := &config.Config{Security: config.SecurityConfig{
		PolicyFile:      path,
		SandboxProvider: "docker",
		DefaultCPU:      1,
		DefaultMemory:   "256Mi",
		DefaultTimeout:  45 * time.Second,
	}}

	if _, err := LoadSecurityPolicy(cfg); err == nil {
		t.Fatal("expected validation error but got nil")
	}
}
