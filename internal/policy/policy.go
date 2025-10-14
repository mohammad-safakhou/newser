package policy

import (
	"fmt"
	"net/url"
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
