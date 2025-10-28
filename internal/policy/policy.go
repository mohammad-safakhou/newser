package policy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
)

// RepeatMode defines how often a topic should run relative to freshness signals.
type RepeatMode string

const (
	// RepeatModeAdaptive triggers runs when content appears stale relative to the dedup window.
	RepeatModeAdaptive RepeatMode = "adaptive"
	// RepeatModeAlways enforces runs on the configured cadence regardless of freshness.
	RepeatModeAlways RepeatMode = "always"
	// RepeatModeManual only runs when explicitly triggered, ignoring cadence heuristics.
	RepeatModeManual RepeatMode = "manual"
)

var validRepeatModes = map[RepeatMode]struct{}{
	RepeatModeAdaptive: {},
	RepeatModeAlways:   {},
	RepeatModeManual:   {},
}

// Valid reports whether the repeat mode is supported.
func (m RepeatMode) Valid() bool {
	_, ok := validRepeatModes[m]
	return ok
}

// UpdatePolicy captures temporal execution preferences supplied by users.
type UpdatePolicy struct {
	RefreshInterval    time.Duration
	DedupWindow        time.Duration
	RepeatMode         RepeatMode
	FreshnessThreshold time.Duration
	Metadata           map[string]interface{}
}

// NewDefault provides a safe fallback policy when none is stored.
func NewDefault() UpdatePolicy {
	return UpdatePolicy{
		RepeatMode: RepeatModeAdaptive,
		Metadata:   map[string]interface{}{},
	}
}

// Validate ensures the policy contains sane values before persistence.
func (p UpdatePolicy) Validate() error {
	if p.RefreshInterval < 0 {
		return fmt.Errorf("refresh interval cannot be negative")
	}
	if p.DedupWindow < 0 {
		return fmt.Errorf("dedup window cannot be negative")
	}
	if p.FreshnessThreshold < 0 {
		return fmt.Errorf("freshness threshold cannot be negative")
	}
	if !p.RepeatMode.Valid() {
		return fmt.Errorf("invalid repeat_mode: %s", p.RepeatMode)
	}
	return nil
}

// Clone returns a deep copy to avoid accidental metadata sharing.
func (p UpdatePolicy) Clone() UpdatePolicy {
	copy := p
	if p.Metadata != nil {
		dup := make(map[string]interface{}, len(p.Metadata))
		for k, v := range p.Metadata {
			dup[k] = v
		}
		copy.Metadata = dup
	}
	return copy
}

// CrawlPolicy encapsulates domain-level crawling rules.
type CrawlPolicy struct {
	RespectRobots bool
	allow         map[string]struct{}
	disallow      map[string]struct{}
	paywall       map[string]struct{}
	attribution   map[string]string
}

// NewCrawlPolicy builds a CrawlPolicy from configuration.
func NewCrawlPolicy(cfg config.CrawlPolicyConfig) (CrawlPolicy, error) {
	if err := cfg.Validate(); err != nil {
		return CrawlPolicy{}, err
	}
	cfg = cfg.Normalize()
	p := CrawlPolicy{
		RespectRobots: cfg.RespectRobots,
		allow:         listToSet(cfg.Allow),
		disallow:      listToSet(cfg.Disallow),
		paywall:       listToSet(cfg.Paywall),
		attribution:   make(map[string]string, len(cfg.Attribution)),
	}
	for host, note := range cfg.Attribution {
		host = normalizeHost(host)
		if host == "" {
			continue
		}
		p.attribution[host] = note
	}
	return p, nil
}

// IsDisallowed reports whether the host is explicitly blocked.
func (p CrawlPolicy) IsDisallowed(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	_, disallowed := p.disallow[host]
	return disallowed && !p.IsAllowed(host)
}

// IsAllowed reports whether the host is explicitly allowed despite default rules.
func (p CrawlPolicy) IsAllowed(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	_, ok := p.allow[host]
	return ok
}

// RequiresPaywall reports whether the host requires paywall handling.
func (p CrawlPolicy) RequiresPaywall(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	_, ok := p.paywall[host]
	return ok
}

// Attribution returns attribution text for the host when available.
func (p CrawlPolicy) Attribution(host string) (string, bool) {
	host = normalizeHost(host)
	if host == "" {
		return "", false
	}
	note, ok := p.attribution[host]
	return note, ok
}

// FairnessPolicy governs credibility adjustments and bias controls.
type FairnessPolicy struct {
	Enabled        bool
	Baseline       float64
	MinCredibility float64
	BiasRange      float64
	DomainBias     map[string]float64
	Alerts         config.FairnessAlerts
}

// NewFairnessPolicy constructs a FairnessPolicy from configuration.
func NewFairnessPolicy(cfg config.FairnessConfig) (*FairnessPolicy, error) {
	if !cfg.Enabled {
		return &FairnessPolicy{Enabled: false}, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	norm := cfg.Normalize()
	if err := norm.Validate(); err != nil {
		return nil, err
	}
	return &FairnessPolicy{
		Enabled:        true,
		Baseline:       clamp01(norm.BaselineCredibility),
		MinCredibility: clamp01(norm.MinCredibility),
		BiasRange:      clamp01(norm.BiasAdjustmentRange),
		DomainBias:     norm.DomainBias,
		Alerts:         norm.Alerts,
	}, nil
}

// BiasFromPreferences extracts a user-provided bias slider from preferences.
func (p *FairnessPolicy) BiasFromPreferences(prefs map[string]interface{}) float64 {
	if p == nil || !p.Enabled || p.BiasRange <= 0 {
		return 0
	}
	bias := extractFloat(prefs["bias_slider"])
	if bias == 0 {
		bias = extractFloat(prefs["fairness_bias"])
	}
	if analysis, ok := prefs["analysis"].(map[string]interface{}); ok && bias == 0 {
		bias = extractFloat(analysis["bias_slider"])
	}
	return clamp(bias, -p.BiasRange, p.BiasRange)
}

// Adjust returns the adjusted credibility and delta applied for a given domain and bias slider.
func (p *FairnessPolicy) Adjust(domain string, credibility float64, bias float64) (float64, float64) {
	if p == nil || !p.Enabled {
		return credibility, 0
	}
	domain = normalizeHost(domain)
	base := credibility
	if base <= 0 {
		base = p.Baseline
	}
	domainBias := p.DomainBias[domain]
	bias = clamp(bias, -p.BiasRange, p.BiasRange)
	delta := clamp(domainBias+bias, -1, 1)
	adjusted := base + delta*(1-base)
	adjusted = clamp(adjusted, 0, 1)
	if adjusted < p.MinCredibility {
		adjusted = p.MinCredibility
	}
	return adjusted, adjusted - credibility
}

func listToSet(items []string) map[string]struct{} {
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		host := normalizeHost(item)
		if host == "" {
			continue
		}
		set[host] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		if u, err := url.Parse(value); err == nil && u.Host != "" {
			value = u.Host
		}
	}
	value = strings.TrimPrefix(value, "www.")
	return value
}

func extractFloat(raw interface{}) float64 {
	switch v := raw.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	}
	return 0
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func clamp01(value float64) float64 {
	return clamp(value, 0, 1)
}

// ExplainabilityPolicy captures toggles for explainability features.
type ExplainabilityPolicy struct {
	EvidenceGraph    bool
	HoverContext     bool
	IncludeMemory    bool
	ShowPlannerTrace bool
	MaxTraceDepth    int
}

// NewExplainabilityPolicy converts config.ExplainabilityConfig into a policy.
func NewExplainabilityPolicy(cfg config.ExplainabilityConfig) (ExplainabilityPolicy, error) {
	norm := cfg.Normalize()
	if err := norm.Validate(); err != nil {
		return ExplainabilityPolicy{}, err
	}
	return ExplainabilityPolicy{
		EvidenceGraph:    norm.EnableEvidenceGraph,
		HoverContext:     norm.EnableHoverContext,
		IncludeMemory:    norm.IncludeMemoryHits,
		ShowPlannerTrace: norm.ShowPlannerTrace,
		MaxTraceDepth:    norm.MaxTraceDepth,
	}, nil
}

// AccessibilityPolicy captures accessibility and localisation defaults.
type AccessibilityPolicy struct {
	MinimumContrast   float64
	FocusRingClass    string
	DefaultLocale     string
	SupportedLocales  []string
	DefaultDateFormat string
	DefaultTimeFormat string
}

// NewAccessibilityPolicy builds a policy from config.AccessibilityConfig.
func NewAccessibilityPolicy(cfg config.AccessibilityConfig) (AccessibilityPolicy, error) {
	norm := cfg.Normalize()
	if err := norm.Validate(); err != nil {
		return AccessibilityPolicy{}, err
	}
	locales := append([]string(nil), norm.SupportedLocales...)
	sort.Strings(locales)
	return AccessibilityPolicy{
		MinimumContrast:   norm.MinimumContrast,
		FocusRingClass:    norm.FocusRingClass,
		DefaultLocale:     norm.DefaultLocale,
		SupportedLocales:  locales,
		DefaultDateFormat: norm.DefaultDateFormat,
		DefaultTimeFormat: norm.DefaultTimeFormat,
	}, nil
}

// SupportsLocale returns true if the locale is recognised by the policy.
func (p AccessibilityPolicy) SupportsLocale(locale string) bool {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return false
	}
	for _, supported := range p.SupportedLocales {
		if strings.EqualFold(supported, locale) {
			return true
		}
	}
	return false
}
