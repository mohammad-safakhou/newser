package policy

import (
	"fmt"
	"time"
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
