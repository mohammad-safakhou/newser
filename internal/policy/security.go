package policy

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"gopkg.in/yaml.v3"
)

// SecurityPolicy captures sandbox execution constraints loaded from policy YAML.
type SecurityPolicy struct {
	Sandbox SandboxPolicy `yaml:"sandbox"`
}

// SandboxPolicy describes sandbox defaults, limits, and allowlists.
type SandboxPolicy struct {
	Provider         string               `yaml:"provider"`
	Image            string               `yaml:"image"`
	ImageAllowlist   []string             `yaml:"image_allowlist"`
	CPU              float64              `yaml:"cpu"`
	MaxCPU           float64              `yaml:"max_cpu"`
	Memory           string               `yaml:"memory"`
	MaxMemory        string               `yaml:"max_memory"`
	Timeout          string               `yaml:"timeout"`
	MaxTimeout       string               `yaml:"max_timeout"`
	Network          SandboxNetworkPolicy `yaml:"network"`
	EnvAllowlist     []string             `yaml:"env_allowlist"`
	EnvDenylist      []string             `yaml:"env_denylist"`
	MountReadOnly    []string             `yaml:"mount_readonly"`
	CommandAllowlist []string             `yaml:"command_allowlist"`
}

// SandboxNetworkPolicy captures outbound network constraints.
type SandboxNetworkPolicy struct {
	Enabled   bool     `yaml:"enabled"`
	Allowlist []string `yaml:"allowlist"`
	Denylist  []string `yaml:"denylist"`
}

// LoadSecurityPolicy loads and validates the security policy YAML.
func LoadSecurityPolicy(cfg *config.Config) (SecurityPolicy, error) {
	if cfg == nil {
		return SecurityPolicy{}, fmt.Errorf("config is nil")
	}
	policyPath := strings.TrimSpace(cfg.Security.PolicyFile)
	if policyPath == "" {
		return SecurityPolicy{}, fmt.Errorf("security.policy_file not configured")
	}
	data, err := os.ReadFile(filepath.Clean(policyPath))
	if err != nil {
		return SecurityPolicy{}, fmt.Errorf("read policy: %w", err)
	}
	var policy SecurityPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return SecurityPolicy{}, fmt.Errorf("parse policy: %w", err)
	}

	policy.Sandbox.applyDefaults(cfg.Security)
	if err := policy.Sandbox.Validate(); err != nil {
		return SecurityPolicy{}, err
	}
	return policy, nil
}

func (s *SandboxPolicy) applyDefaults(cfg config.SecurityConfig) {
	if s.Provider == "" {
		s.Provider = cfg.SandboxProvider
	}
	if s.CPU <= 0 {
		s.CPU = cfg.DefaultCPU
	}
	if s.MaxCPU <= 0 {
		s.MaxCPU = cfg.DefaultCPU
	}
	if s.MaxCPU < s.CPU {
		s.MaxCPU = s.CPU
	}

	s.Memory = strings.TrimSpace(firstNonEmpty(s.Memory, cfg.DefaultMemory))
	s.MaxMemory = strings.TrimSpace(firstNonEmpty(s.MaxMemory, s.Memory))

	if s.Timeout == "" && cfg.DefaultTimeout > 0 {
		s.Timeout = cfg.DefaultTimeout.String()
	}
	if s.MaxTimeout == "" {
		s.MaxTimeout = s.Timeout
	}

	// Ensure image allowlist includes explicit default image when provided.
	if s.Image != "" && len(s.ImageAllowlist) == 0 {
		s.ImageAllowlist = []string{s.Image}
	}
	s.ImageAllowlist = sanitizeList(s.ImageAllowlist)
	s.CommandAllowlist = sanitizeList(s.CommandAllowlist)
	s.EnvAllowlist = sanitizeList(s.EnvAllowlist)
	s.EnvDenylist = sanitizeList(s.EnvDenylist)
	s.MountReadOnly = sanitizeList(s.MountReadOnly)

	s.Network.Allowlist = sanitizeHosts(s.Network.Allowlist)
	s.Network.Denylist = sanitizeHosts(s.Network.Denylist)
}

// Validate ensures the sandbox policy contains sane values.
func (s SandboxPolicy) Validate() error {
	if strings.TrimSpace(s.Provider) == "" {
		return fmt.Errorf("sandbox provider is required")
	}
	if s.CPU <= 0 {
		return fmt.Errorf("sandbox cpu must be greater than zero")
	}
	if s.MaxCPU < s.CPU {
		return fmt.Errorf("sandbox max_cpu %.2f cannot be less than cpu %.2f", s.MaxCPU, s.CPU)
	}
	if _, err := parseMemoryString(s.Memory); err != nil {
		return fmt.Errorf("sandbox memory invalid: %w", err)
	}
	if maxBytes, err := parseMemoryString(s.MaxMemory); err != nil {
		return fmt.Errorf("sandbox max_memory invalid: %w", err)
	} else if maxBytes > 0 {
		if memBytes, _ := parseMemoryString(s.Memory); memBytes > 0 && memBytes > maxBytes {
			return fmt.Errorf("sandbox memory %s exceeds max_memory %s", s.Memory, s.MaxMemory)
		}
	}
	if _, err := parseDurationString(s.Timeout); err != nil {
		return fmt.Errorf("sandbox timeout invalid: %w", err)
	}
	if maxTimeout, err := parseDurationString(s.MaxTimeout); err != nil {
		return fmt.Errorf("sandbox max_timeout invalid: %w", err)
	} else if maxTimeout > 0 {
		if timeout, _ := parseDurationString(s.Timeout); timeout > maxTimeout {
			return fmt.Errorf("sandbox timeout %s exceeds max_timeout %s", s.Timeout, s.MaxTimeout)
		}
	}
	for _, cmd := range s.CommandAllowlist {
		if cmd == "" {
			return fmt.Errorf("sandbox command_allowlist contains empty entry")
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sanitizeList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	var out []string
	for _, raw := range items {
		val := strings.TrimSpace(raw)
		if val == "" {
			continue
		}
		valLower := strings.ToLower(val)
		if _, ok := seen[valLower]; ok {
			continue
		}
		seen[valLower] = struct{}{}
		out = append(out, val)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeHosts(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		host := normalizeHost(raw)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseMemoryString(value string) (float64, error) {
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
			f, err := parseFloat(number)
			if err != nil {
				return 0, err
			}
			return f * multiplier, nil
		}
	}
	f, err := parseFloat(val)
	if err != nil {
		return 0, err
	}
	return f, nil
}

func parseDurationString(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func parseFloat(value string) (float64, error) {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", value)
	}
	return f, nil
}
