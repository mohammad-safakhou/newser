package budget

import "fmt"

// Config defines budget guardrails for a topic or run.
type Config struct {
	MaxCost           *float64
	MaxTokens         *int64
	MaxTimeSeconds    *int64
	ApprovalThreshold *float64
	RequireApproval   bool
	Metadata          map[string]interface{}
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
