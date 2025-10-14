package budget

import (
	"fmt"
	"time"

	"github.com/mohammad-safakhou/newser/internal/planner"
)

// Config defines budget guardrails for a topic or run.
type Config struct {
	MaxCost           *float64
	MaxTokens         *int64
	MaxTimeSeconds    *int64
	ApprovalThreshold *float64
	RequireApproval   bool
	Metadata          map[string]interface{}
}

// Estimate captures the planner's predicted cost, tokens, and duration for a run.
type Estimate struct {
	Cost     float64
	Tokens   int64
	Duration time.Duration
}

// Usage captures the actual consumption recorded by the budget monitor.
type Usage struct {
	Cost    float64
	Tokens  int64
	Elapsed time.Duration
}

// ErrBreach wraps an ErrExceeded with the observed usage metrics.
type ErrBreach struct {
	ErrExceeded
	Usage Usage
}

// Error satisfies the error interface.
func (e ErrBreach) Error() string { return e.ErrExceeded.Error() }

// Unwrap exposes the underlying ErrExceeded for errors.As.
func (e ErrBreach) Unwrap() error { return e.ErrExceeded }

// NewBreach constructs an ErrBreach from the exceeded error and usage snapshot.
func NewBreach(err ErrExceeded, usage Usage) ErrBreach {
	return ErrBreach{ErrExceeded: err, Usage: usage}
}

// Validate ensures the budget values are sane before use.
func (c Config) Validate() error {
	if c.MaxCost != nil && *c.MaxCost < 0 {
		return fmt.Errorf("max_cost cannot be negative")
	}
	if c.MaxTokens != nil && *c.MaxTokens < 0 {
		return fmt.Errorf("max_tokens cannot be negative")
	}
	if c.MaxTimeSeconds != nil && *c.MaxTimeSeconds < 0 {
		return fmt.Errorf("max_time_seconds cannot be negative")
	}
	if c.ApprovalThreshold != nil {
		if *c.ApprovalThreshold < 0 {
			return fmt.Errorf("approval_threshold cannot be negative")
		}
		if c.MaxCost != nil && *c.ApprovalThreshold > *c.MaxCost {
			return fmt.Errorf("approval_threshold cannot exceed max_cost")
		}
	}
	return nil
}

// Clone produces a deep copy of the config.
func (c Config) Clone() Config {
	clone := Config{
		RequireApproval: c.RequireApproval,
	}
	if c.MaxCost != nil {
		v := *c.MaxCost
		clone.MaxCost = &v
	}
	if c.MaxTokens != nil {
		v := *c.MaxTokens
		clone.MaxTokens = &v
	}
	if c.MaxTimeSeconds != nil {
		v := *c.MaxTimeSeconds
		clone.MaxTimeSeconds = &v
	}
	if c.ApprovalThreshold != nil {
		v := *c.ApprovalThreshold
		clone.ApprovalThreshold = &v
	}
	if c.Metadata != nil {
		clone.Metadata = make(map[string]interface{}, len(c.Metadata))
		for k, v := range c.Metadata {
			clone.Metadata[k] = v
		}
	}
	return clone
}

// Merge overlays non-nil values from override onto base.
func Merge(base Config, override Config) Config {
	result := base.Clone()
	if override.MaxCost != nil {
		v := *override.MaxCost
		result.MaxCost = &v
	}
	if override.MaxTokens != nil {
		v := *override.MaxTokens
		result.MaxTokens = &v
	}
	if override.MaxTimeSeconds != nil {
		v := *override.MaxTimeSeconds
		result.MaxTimeSeconds = &v
	}
	if override.ApprovalThreshold != nil {
		v := *override.ApprovalThreshold
		result.ApprovalThreshold = &v
	}
	if override.Metadata != nil {
		result.Metadata = make(map[string]interface{}, len(override.Metadata))
		for k, v := range override.Metadata {
			result.Metadata[k] = v
		}
	}
	if override.RequireApproval {
		result.RequireApproval = true
	}
	return result
}

// IsZero reports whether the config defines no explicit limits or requirements.
func (c Config) IsZero() bool {
	if c.MaxCost != nil && *c.MaxCost != 0 {
		return false
	}
	if c.MaxTokens != nil && *c.MaxTokens != 0 {
		return false
	}
	if c.MaxTimeSeconds != nil && *c.MaxTimeSeconds != 0 {
		return false
	}
	if c.ApprovalThreshold != nil && *c.ApprovalThreshold != 0 {
		return false
	}
	if c.RequireApproval {
		return false
	}
	return len(c.Metadata) == 0
}

// RequiresApproval returns true when approval is mandatory based on config and estimates.
func RequiresApproval(cfg Config, estimatedCost float64) bool {
	if cfg.RequireApproval {
		return true
	}
	if cfg.ApprovalThreshold != nil && estimatedCost > *cfg.ApprovalThreshold {
		return true
	}
	return false
}

// EstimateFromPlan derives an Estimate from a planner document, falling back to
// task-level estimates when aggregate totals are absent.
func EstimateFromPlan(doc *planner.PlanDocument) Estimate {
	var out Estimate
	if doc == nil {
		return out
	}
	if doc.Estimates != nil {
		out.Cost = doc.Estimates.TotalCost
		out.Tokens = doc.Estimates.TotalTokens
		if doc.Estimates.TotalTime != "" {
			if d, err := time.ParseDuration(doc.Estimates.TotalTime); err == nil {
				out.Duration = d
			}
		}
	}
	// backfill with task-level sums if totals were missing or zero.
	var (
		sumCost   float64
		sumTokens int64
		sumDur    time.Duration
	)
	for _, task := range doc.Tasks {
		sumCost += task.EstimatedCost
		sumTokens += int64(task.EstimatedTokens)
		if task.EstimatedTime != "" {
			if d, err := time.ParseDuration(task.EstimatedTime); err == nil {
				sumDur += d
			}
		}
	}
	if out.Cost == 0 && sumCost > 0 {
		out.Cost = sumCost
	}
	if out.Tokens == 0 && sumTokens > 0 {
		out.Tokens = sumTokens
	}
	if out.Duration == 0 && sumDur > 0 {
		out.Duration = sumDur
	}
	return out
}

// UsageFromMonitor converts monitor metrics into a Usage summary.
func UsageFromMonitor(m *Monitor) Usage {
	if m == nil {
		return Usage{}
	}
	cost, tokens, elapsed := m.Usage()
	return Usage{Cost: cost, Tokens: tokens, Elapsed: elapsed}
}

// SerializeConfig renders a budget config as a JSON-friendly map.
func SerializeConfig(cfg Config) map[string]interface{} {
	out := make(map[string]interface{})
	if cfg.MaxCost != nil {
		out["max_cost"] = *cfg.MaxCost
	}
	if cfg.MaxTokens != nil {
		out["max_tokens"] = *cfg.MaxTokens
	}
	if cfg.MaxTimeSeconds != nil {
		out["max_time_seconds"] = *cfg.MaxTimeSeconds
	}
	if cfg.ApprovalThreshold != nil {
		out["approval_threshold"] = *cfg.ApprovalThreshold
	}
	out["require_approval"] = cfg.RequireApproval
	if len(cfg.Metadata) > 0 {
		meta := make(map[string]interface{}, len(cfg.Metadata))
		for k, v := range cfg.Metadata {
			meta[k] = v
		}
		out["metadata"] = meta
	}
	return out
}

// SerializeEstimate renders an Estimate for telemetry/metadata.
func SerializeEstimate(est Estimate) map[string]interface{} {
	out := make(map[string]interface{})
	if est.Cost != 0 {
		out["cost"] = est.Cost
	}
	if est.Tokens != 0 {
		out["tokens"] = est.Tokens
	}
	if est.Duration > 0 {
		out["time_seconds"] = est.Duration.Seconds()
	}
	return out
}

// SerializeUsage renders usage metrics for metadata.
func SerializeUsage(usage Usage) map[string]interface{} {
	out := make(map[string]interface{})
	if usage.Cost != 0 {
		out["cost"] = usage.Cost
	}
	if usage.Tokens != 0 {
		out["tokens"] = usage.Tokens
	}
	if usage.Elapsed > 0 {
		out["time_seconds"] = usage.Elapsed.Seconds()
	}
	return out
}

// SerializeBreach summarises a budget breach with numeric usage data.
func SerializeBreach(cfg Config, usage Usage, exceeded ErrExceeded) map[string]interface{} {
	summary := map[string]interface{}{
		"kind":    exceeded.Kind,
		"message": exceeded.Error(),
	}
	switch exceeded.Kind {
	case "cost":
		summary["usage_cost"] = usage.Cost
		if cfg.MaxCost != nil {
			summary["limit_cost"] = *cfg.MaxCost
		}
	case "tokens":
		summary["usage_tokens"] = usage.Tokens
		if cfg.MaxTokens != nil {
			summary["limit_tokens"] = *cfg.MaxTokens
		}
	case "time":
		summary["usage_seconds"] = usage.Elapsed.Seconds()
		if cfg.MaxTimeSeconds != nil {
			summary["limit_seconds"] = *cfg.MaxTimeSeconds
		}
	}
	if exceeded.Limit != "" {
		summary["limit_label"] = exceeded.Limit
	}
	summary["usage_label"] = exceeded.Usage
	return summary
}

// BuildReport assembles a consolidated budget report combining config,
// estimate, usage, and optional breach summaries.
func BuildReport(cfg Config, estimate Estimate, usage Usage, breaches ...ErrExceeded) map[string]interface{} {
	report := map[string]interface{}{
		"config":   SerializeConfig(cfg),
		"estimate": SerializeEstimate(estimate),
		"usage":    SerializeUsage(usage),
	}
	if len(breaches) > 0 {
		summaries := make([]map[string]interface{}, 0, len(breaches))
		for _, breach := range breaches {
			summaries = append(summaries, SerializeBreach(cfg, usage, breach))
		}
		report["breaches"] = summaries
	}
	return report
}

// FormatBreachReason builds a human-readable reason string for storage/audit logs.
func FormatBreachReason(cfg Config, usage Usage, exceeded ErrExceeded) string {
	summary := SerializeBreach(cfg, usage, exceeded)
	reason := exceeded.Error()
	if cost, ok := summary["usage_cost"].(float64); ok {
		reason += fmt.Sprintf(" (cost=%.2f", cost)
		if limit, ok := summary["limit_cost"].(float64); ok {
			reason += fmt.Sprintf(" limit=%.2f", limit)
		}
		reason += ")"
	} else if tokens, ok := summary["usage_tokens"].(int64); ok {
		reason += fmt.Sprintf(" (tokens=%d", tokens)
		if limit, ok := summary["limit_tokens"].(int64); ok {
			reason += fmt.Sprintf(" limit=%d", limit)
		}
		reason += ")"
	} else if seconds, ok := summary["usage_seconds"].(float64); ok {
		reason += fmt.Sprintf(" (elapsed=%.2fs", seconds)
		if limit, ok := summary["limit_seconds"].(float64); ok {
			reason += fmt.Sprintf(" limit=%.2fs", limit)
		}
		reason += ")"
	}
	return reason
}
