package runtime

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
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

var (
	sandboxMetricsOnce      sync.Once
	sandboxRequests         otelmetric.Int64Counter
	sandboxCPUHistogram     otelmetric.Float64Histogram
	sandboxTimeoutHistogram otelmetric.Float64Histogram
	sandboxMemoryHistogram  otelmetric.Float64Histogram
	sandboxNetworkAllowed   otelmetric.Int64Counter
	sandboxNetworkBlocked   otelmetric.Int64Counter
)

func initSandboxMetrics() {
	meter := otel.Meter("newser/runtime/sandbox")
	var err error
	sandboxRequests, err = meter.Int64Counter(
		"sandbox_requests_total",
		otelmetric.WithDescription("Number of sandbox validations performed"),
	)
	if err != nil {
		log.Printf("sandbox metrics init: requests counter: %v", err)
	}
	sandboxCPUHistogram, err = meter.Float64Histogram(
		"sandbox_request_cpu",
		otelmetric.WithDescription("Requested CPU cores for sandboxed execution"),
		otelmetric.WithUnit("cores"),
	)
	if err != nil {
		log.Printf("sandbox metrics init: cpu histogram: %v", err)
	}
	sandboxTimeoutHistogram, err = meter.Float64Histogram(
		"sandbox_request_timeout_seconds",
		otelmetric.WithDescription("Requested timeout for sandboxed execution"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		log.Printf("sandbox metrics init: timeout histogram: %v", err)
	}
	sandboxMemoryHistogram, err = meter.Float64Histogram(
		"sandbox_request_memory_bytes",
		otelmetric.WithDescription("Requested memory limit for sandboxed execution"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		log.Printf("sandbox metrics init: memory histogram: %v", err)
	}
	sandboxNetworkAllowed, err = meter.Int64Counter(
		"sandbox_network_enabled_total",
		otelmetric.WithDescription("Sandbox executions where outbound network was enabled"),
	)
	if err != nil {
		log.Printf("sandbox metrics init: network enabled counter: %v", err)
	}
	sandboxNetworkBlocked, err = meter.Int64Counter(
		"sandbox_network_blocked_total",
		otelmetric.WithDescription("Sandbox executions where outbound network was blocked"),
	)
	if err != nil {
		log.Printf("sandbox metrics init: network blocked counter: %v", err)
	}
}

func NewSandboxEnforcer(policy *SandboxPolicy) *SandboxEnforcer {
	return &SandboxEnforcer{policy: policy}
}

// Validate ensures settings meet policy requirements and applies default values
// from the loaded policy to the supplied request. The request is mutated in
// place so callers can rely on the returned values for downstream execution.
func (e *SandboxEnforcer) Validate(ctx context.Context, req *SandboxRequest) error {
	if e == nil || e.policy == nil {
		return nil
	}
	if req == nil {
		return fmt.Errorf("sandbox request is nil")
	}
	if req.Provider == "" {
		req.Provider = e.policy.Provider
	} else if req.Provider != e.policy.Provider {
		return fmt.Errorf("provider %s not allowed (configured %s)", req.Provider, e.policy.Provider)
	}
	if req.CPU <= 0 {
		req.CPU = e.policy.CPU
	}
	if req.CPU > e.policy.CPU {
		return fmt.Errorf("cpu %.2f exceeds policy %.2f", req.CPU, e.policy.CPU)
	}
	if req.Timeout <= 0 {
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
func EnsureSandbox(ctx context.Context, cfg *config.Config, service string, logger *log.Logger, req SandboxRequest) (*SandboxEnforcer, SandboxRequest, error) {
	policy, err := LoadSandboxPolicy(cfg)
	if err != nil {
		return nil, SandboxRequest{}, err
	}

	enforcer := NewSandboxEnforcer(policy)
	normalized := req
	if err := enforcer.Validate(ctx, &normalized); err != nil {
		return nil, SandboxRequest{}, err
	}

	if logger == nil {
		prefix := strings.TrimSpace(service)
		if prefix == "" {
			prefix = "service"
		}
		logger = log.New(os.Stdout, fmt.Sprintf("[%s] ", strings.ToUpper(prefix)), log.LstdFlags)
	}

	logger.Printf("sandbox=true provider=%s cpu=%.2f memory=%s timeout=%s network_enabled=%t allowlist=%d", normalized.Provider, normalized.CPU, policy.Memory, normalized.Timeout, policy.Network.Enabled, len(policy.Network.Allowlist))

	recordSandboxMetrics(ctx, service, policy, normalized)

	return enforcer, normalized, nil
}

func recordSandboxMetrics(ctx context.Context, service string, policy *SandboxPolicy, normalized SandboxRequest) {
	if ctx == nil {
		ctx = context.Background()
	}
	sandboxMetricsOnce.Do(initSandboxMetrics)
	attrs := []attribute.KeyValue{
		attribute.String("service", strings.TrimSpace(service)),
		attribute.String("provider", strings.TrimSpace(policy.Provider)),
	}
	if sandboxRequests != nil {
		sandboxRequests.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
	}
	if sandboxCPUHistogram != nil {
		sandboxCPUHistogram.Record(ctx, normalized.CPU, otelmetric.WithAttributes(attrs...))
	}
	if sandboxTimeoutHistogram != nil && normalized.Timeout > 0 {
		sandboxTimeoutHistogram.Record(ctx, normalized.Timeout.Seconds(), otelmetric.WithAttributes(attrs...))
	}
	if sandboxMemoryHistogram != nil {
		if memBytes := parseMemoryBytes(policy.Memory); memBytes > 0 {
			sandboxMemoryHistogram.Record(ctx, memBytes, otelmetric.WithAttributes(attrs...))
		}
	}
	if normalized.NetworkEnabled {
		if sandboxNetworkAllowed != nil {
			sandboxNetworkAllowed.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
		}
	} else if !policy.Network.Enabled {
		if sandboxNetworkBlocked != nil {
			sandboxNetworkBlocked.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
		}
	}
}

func parseMemoryBytes(value string) float64 {
	val := strings.TrimSpace(strings.ToLower(value))
	if val == "" {
		return 0
	}
	unitMultipliers := map[string]float64{
		"kb":  1024,
		"k":   1024,
		"kib": 1024,
		"mb":  1024 * 1024,
		"m":   1024 * 1024,
		"mib": 1024 * 1024,
		"gb":  1024 * 1024 * 1024,
		"g":   1024 * 1024 * 1024,
		"gib": 1024 * 1024 * 1024,
		"tb":  1024 * 1024 * 1024 * 1024,
		"t":   1024 * 1024 * 1024 * 1024,
		"tib": 1024 * 1024 * 1024 * 1024,
		"pb":  math.Pow(1024, 5),
		"p":   math.Pow(1024, 5),
		"pib": math.Pow(1024, 5),
		"b":   1,
	}

	for unit, multiplier := range unitMultipliers {
		if strings.HasSuffix(val, unit) {
			number := strings.TrimSpace(strings.TrimSuffix(val, unit))
			f, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0
			}
			return f * multiplier
		}
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}
	return 0
}
