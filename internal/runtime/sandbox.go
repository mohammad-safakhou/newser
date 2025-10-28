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
	"github.com/mohammad-safakhou/newser/internal/policy"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// SandboxPolicy represents runtime sandbox settings.
type SandboxPolicy struct {
	Provider       string
	Image          string
	ImageAllowlist []string
	CPU            float64
	MaxCPU         float64
	Memory         string
	MaxMemory      string
	Timeout        time.Duration
	MaxTimeout     time.Duration
	Network        struct {
		Enabled   bool
		Allowlist []string
		Denylist  []string
	}
	EnvAllowlist     []string
	EnvDenylist      []string
	MountReadOnly    []string
	CommandAllowlist []string
}

// LoadSandboxPolicy reads policy from config.Security.PolicyFile.
func LoadSandboxPolicy(cfg *config.Config) (*SandboxPolicy, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	rawPolicy, err := policy.LoadSecurityPolicy(cfg)
	if err != nil {
		return nil, err
	}

	timeout, err := parseDuration(rawPolicy.Sandbox.Timeout)
	if err != nil {
		return nil, fmt.Errorf("sandbox timeout invalid: %w", err)
	}
	maxTimeout, err := parseDuration(rawPolicy.Sandbox.MaxTimeout)
	if err != nil {
		return nil, fmt.Errorf("sandbox max timeout invalid: %w", err)
	}

	sandbox := &SandboxPolicy{
		Provider:         rawPolicy.Sandbox.Provider,
		Image:            rawPolicy.Sandbox.Image,
		ImageAllowlist:   append([]string(nil), rawPolicy.Sandbox.ImageAllowlist...),
		CPU:              rawPolicy.Sandbox.CPU,
		MaxCPU:           rawPolicy.Sandbox.MaxCPU,
		Memory:           rawPolicy.Sandbox.Memory,
		MaxMemory:        rawPolicy.Sandbox.MaxMemory,
		Timeout:          timeout,
		MaxTimeout:       maxTimeout,
		EnvAllowlist:     append([]string(nil), rawPolicy.Sandbox.EnvAllowlist...),
		EnvDenylist:      append([]string(nil), rawPolicy.Sandbox.EnvDenylist...),
		MountReadOnly:    append([]string(nil), rawPolicy.Sandbox.MountReadOnly...),
		CommandAllowlist: append([]string(nil), rawPolicy.Sandbox.CommandAllowlist...),
	}
	sandbox.Network.Enabled = rawPolicy.Sandbox.Network.Enabled
	sandbox.Network.Allowlist = append([]string(nil), rawPolicy.Sandbox.Network.Allowlist...)
	sandbox.Network.Denylist = append([]string(nil), rawPolicy.Sandbox.Network.Denylist...)

	if sandbox.Provider == "" {
		return nil, fmt.Errorf("sandbox provider missing; set security.sandbox_provider or sandbox.provider in policy")
	}
	return sandbox, nil
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
	if len(e.policy.ImageAllowlist) > 0 {
		image := strings.TrimSpace(req.Image)
		if image == "" {
			image = e.policy.Image
			if image == "" {
				image = e.policy.ImageAllowlist[0]
			}
		}
		if !containsStringFold(e.policy.ImageAllowlist, image) {
			return fmt.Errorf("image %q not allowed by sandbox policy", image)
		}
		req.Image = image
	} else if req.Image == "" && e.policy.Image != "" {
		req.Image = e.policy.Image
	}
	cpuLimit := e.policy.MaxCPU
	if cpuLimit <= 0 {
		cpuLimit = e.policy.CPU
	}
	if req.CPU <= 0 {
		req.CPU = e.policy.CPU
	}
	if req.CPU > cpuLimit {
		return fmt.Errorf("cpu %.2f exceeds policy limit %.2f", req.CPU, cpuLimit)
	}
	if strings.TrimSpace(req.Memory) == "" {
		req.Memory = e.policy.Memory
	}
	if limit := strings.TrimSpace(e.policy.MaxMemory); limit != "" {
		reqBytes, err := parseMemoryBytes(req.Memory)
		if err != nil {
			return fmt.Errorf("memory %q invalid: %w", req.Memory, err)
		}
		limitBytes, err := parseMemoryBytes(limit)
		if err != nil {
			return fmt.Errorf("policy max_memory invalid: %w", err)
		}
		if limitBytes > 0 && reqBytes > limitBytes {
			return fmt.Errorf("memory %s exceeds policy limit %s", req.Memory, limit)
		}
	}
	if req.Timeout <= 0 {
		req.Timeout = e.policy.Timeout
	}
	if e.policy.MaxTimeout > 0 && req.Timeout > e.policy.MaxTimeout {
		return fmt.Errorf("timeout %s exceeds policy limit %s", req.Timeout, e.policy.MaxTimeout)
	}
	if len(e.policy.CommandAllowlist) > 0 {
		command := strings.TrimSpace(req.Command)
		if command != "" && !containsStringFold(e.policy.CommandAllowlist, command) {
			return fmt.Errorf("command %q not permitted by sandbox policy", command)
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
	Memory         string
	Timeout        time.Duration
	NetworkEnabled bool
	Image          string
	Command        string
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

	cpuLimit := policy.MaxCPU
	if cpuLimit <= 0 {
		cpuLimit = policy.CPU
	}
	logger.Printf(
		"sandbox=true provider=%s cpu=%.2f limit=%.2f memory=%s limit=%s timeout=%s limit=%s network_enabled=%t allowlist=%d",
		normalized.Provider,
		normalized.CPU,
		cpuLimit,
		normalized.Memory,
		formatMemoryLimit(policy.MaxMemory),
		normalized.Timeout,
		formatDurationLimit(policy.MaxTimeout),
		normalized.NetworkEnabled,
		len(policy.Network.Allowlist),
	)

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
		if memBytes, err := parseMemoryBytes(normalized.Memory); err == nil && memBytes > 0 {
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

func parseMemoryBytes(value string) (float64, error) {
	val := strings.TrimSpace(strings.ToLower(value))
	if val == "" {
		return 0, nil
	}
	unitMultipliers := map[string]float64{
		"kb":  1024,
		"k":   1024,
		"kib": 1024,
		"mb":  1024 * 1024,
		"m":   1024 * 1024,
		"mi":  1024 * 1024,
		"mib": 1024 * 1024,
		"gb":  1024 * 1024 * 1024,
		"g":   1024 * 1024 * 1024,
		"gi":  1024 * 1024 * 1024,
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
			if number == "" {
				return 0, fmt.Errorf("memory value missing quantity")
			}
			f, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid memory quantity %q", number)
			}
			return f * multiplier, nil
		}
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value %q", value)
	}
	return f, nil
}

func parseDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func containsStringFold(list []string, candidate string) bool {
	if len(list) == 0 {
		return false
	}
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func formatMemoryLimit(limit string) string {
	if strings.TrimSpace(limit) == "" {
		return "unlimited"
	}
	return limit
}

func formatDurationLimit(limit time.Duration) string {
	if limit <= 0 {
		return "unlimited"
	}
	return limit.String()
}
