package config

import (
	"fmt"
	"strings"
)

// Normalize clamps configuration values and standardises keys.
func (c FairnessConfig) Normalize() FairnessConfig {
	cfg := c
	if cfg.BaselineCredibility <= 0 {
		cfg.BaselineCredibility = 0.5
	}
	if cfg.BaselineCredibility > 1 {
		cfg.BaselineCredibility = 1
	}
	if cfg.MinCredibility < 0 {
		cfg.MinCredibility = 0
	}
	if cfg.MinCredibility > 1 {
		cfg.MinCredibility = 1
	}
	if cfg.BiasAdjustmentRange < 0 {
		cfg.BiasAdjustmentRange = 0
	}
	if cfg.BiasAdjustmentRange > 1 {
		cfg.BiasAdjustmentRange = 1
	}
	if cfg.DomainBias == nil {
		cfg.DomainBias = map[string]float64{}
	}
	domainBias := make(map[string]float64, len(cfg.DomainBias))
	for host, value := range cfg.DomainBias {
		key := strings.TrimSpace(strings.ToLower(host))
		if key == "" {
			continue
		}
		if value < -1 {
			value = -1
		}
		if value > 1 {
			value = 1
		}
		domainBias[key] = value
	}
	cfg.DomainBias = domainBias
	if cfg.Alerts.LowCredibility < 0 {
		cfg.Alerts.LowCredibility = 0
	}
	if cfg.Alerts.LowCredibility > 1 {
		cfg.Alerts.LowCredibility = 1
	}
	if cfg.Alerts.HighBias < 0 {
		cfg.Alerts.HighBias = 0
	}
	if cfg.Alerts.HighBias > 1 {
		cfg.Alerts.HighBias = 1
	}
	return cfg
}

// Validate ensures configuration is internally consistent.
func (c FairnessConfig) Validate() error {
	if c.BaselineCredibility <= 0 {
		return fmt.Errorf("baseline_credibility must be positive")
	}
	if c.MinCredibility < 0 || c.MinCredibility > 1 {
		return fmt.Errorf("min_credibility must be between 0 and 1")
	}
	if c.BiasAdjustmentRange < 0 || c.BiasAdjustmentRange > 1 {
		return fmt.Errorf("bias_adjustment_range must be between 0 and 1")
	}
	return nil
}
