package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"gopkg.in/yaml.v3"
)

// SandboxPolicy represents runtime sandbox settings.
type SandboxPolicy struct {
	Provider string  `yaml:"provider"`
	Image    string  `yaml:"image"`
	CPU      float64 `yaml:"cpu"`
	Memory   string  `yaml:"memory"`
	Timeout  string  `yaml:"timeout"`
	Network  struct {
		Enabled   bool     `yaml:"enabled"`
		Allowlist []string `yaml:"allowlist"`
	} `yaml:"network"`
	EnvAllowlist  []string `yaml:"env_allowlist"`
	MountReadOnly []string `yaml:"mount_readonly"`
}

// LoadSandboxPolicy reads policy from config.Security.PolicyFile.
func LoadSandboxPolicy(cfg *config.Config) (*SandboxPolicy, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	policyPath := cfg.Security.PolicyFile
	if policyPath == "" {
		return nil, fmt.Errorf("security.policy_file not configured")
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}
	var policy struct {
		Sandbox SandboxPolicy `yaml:"sandbox"`
	}
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	if policy.Sandbox.Provider == "" {
		policy.Sandbox.Provider = cfg.Security.SandboxProvider
	}
	if policy.Sandbox.Timeout == "" {
		policy.Sandbox.Timeout = cfg.Security.DefaultTimeout.String()
	}
	if policy.Sandbox.CPU == 0 {
		policy.Sandbox.CPU = cfg.Security.DefaultCPU
	}
	if policy.Sandbox.Memory == "" {
		policy.Sandbox.Memory = cfg.Security.DefaultMemory
	}
	if policy.Sandbox.Provider == "" {
		return nil, fmt.Errorf("sandbox provider missing; set security.sandbox_provider or sandbox.provider in policy")
	}
	return &policy.Sandbox, nil
}

// SandboxEnforcer performs policy validation prior to execution.
type SandboxEnforcer struct {
	policy *SandboxPolicy
}

func NewSandboxEnforcer(policy *SandboxPolicy) *SandboxEnforcer {
	return &SandboxEnforcer{policy: policy}
}

// Validate ensures settings meet policy requirements.
func (e *SandboxEnforcer) Validate(ctx context.Context, req SandboxRequest) error {
	if e == nil || e.policy == nil {
		return nil
	}
	if req.Provider != "" && req.Provider != e.policy.Provider {
		return fmt.Errorf("provider %s not allowed (configured %s)", req.Provider, e.policy.Provider)
	}
	if req.Provider == "" && e.policy.Provider != "" {
		req.Provider = e.policy.Provider
	}
	if req.CPU <= 0 {
		req.CPU = e.policy.CPU
	}
	if req.CPU > e.policy.CPU {
		return fmt.Errorf("cpu %.2f exceeds policy %.2f", req.CPU, e.policy.CPU)
	}
	if req.Timeout <= 0 {
		// Use policy timeout
		if d, err := time.ParseDuration(e.policy.Timeout); err == nil {
			req.Timeout = d
		}
	}
	if req.Timeout > 0 {
		if d, err := time.ParseDuration(e.policy.Timeout); err == nil && req.Timeout > d {
			return fmt.Errorf("timeout %s exceeds policy %s", req.Timeout, d)
		}
	}
	if !e.policy.Network.Enabled && req.NetworkEnabled {
		return fmt.Errorf("network access disabled by policy")
	}
	return nil
}

// SandboxRequest describes an execution request for validation.
type SandboxRequest struct {
	Provider       string
	CPU            float64
	Timeout        time.Duration
	NetworkEnabled bool
}

// Policy returns the underlying policy, useful for diagnostics and logging.
func (e *SandboxEnforcer) Policy() *SandboxPolicy {
	if e == nil {
		return nil
	}
	return e.policy
}

// EnsureSandbox loads the sandbox policy, validates the provided request against
// the policy and logs a standard "sandbox=true" message for observability.
func EnsureSandbox(ctx context.Context, cfg *config.Config, service string, logger *log.Logger, req SandboxRequest) (*SandboxEnforcer, error) {
	policy, err := LoadSandboxPolicy(cfg)
	if err != nil {
		return nil, err
	}

	enforcer := NewSandboxEnforcer(policy)
	if err := enforcer.Validate(ctx, req); err != nil {
		return nil, err
	}

	if logger == nil {
		prefix := strings.TrimSpace(service)
		if prefix == "" {
			prefix = "service"
		}
		logger = log.New(os.Stdout, fmt.Sprintf("[%s] ", strings.ToUpper(prefix)), log.LstdFlags)
	}

	logger.Printf("sandbox=true provider=%s cpu=%.2f memory=%s timeout=%s network_enabled=%t allowlist=%d", policy.Provider, policy.CPU, policy.Memory, policy.Timeout, policy.Network.Enabled, len(policy.Network.Allowlist))

	return enforcer, nil
}
