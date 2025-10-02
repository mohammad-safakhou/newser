package budget

import (
	"fmt"
	"sync"
	"time"
)

// Monitor tracks actual usage against configured limits during execution.
type Monitor struct {
	config     Config
	costUsed   float64
	tokensUsed int64
	startTime  time.Time
	mu         sync.Mutex
}

// NewMonitor clones the provided config and starts tracking usage.
func NewMonitor(cfg Config) *Monitor {
	return &Monitor{
		config:    cfg.Clone(),
		startTime: time.Now(),
	}
}

// Add records incremental cost and tokens, returning an error if any limit is breached.
func (m *Monitor) Add(cost float64, tokens int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.costUsed += cost
	m.tokensUsed += tokens
	if m.config.MaxCost != nil && m.costUsed > *m.config.MaxCost {
		return ErrExceeded{
			Kind:  "cost",
			Usage: fmt.Sprintf("$%.4f", m.costUsed),
			Limit: fmt.Sprintf("$%.4f", *m.config.MaxCost),
		}
	}
	if m.config.MaxTokens != nil && m.tokensUsed > *m.config.MaxTokens {
		return ErrExceeded{
			Kind:  "tokens",
			Usage: fmt.Sprintf("%d tokens", m.tokensUsed),
			Limit: fmt.Sprintf("%d tokens", *m.config.MaxTokens),
		}
	}
	return nil
}

// CheckTime verifies elapsed time against the configured limit.
func (m *Monitor) CheckTime() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config.MaxTimeSeconds == nil || *m.config.MaxTimeSeconds <= 0 {
		return nil
	}
	elapsed := time.Since(m.startTime)
	limit := time.Duration(*m.config.MaxTimeSeconds) * time.Second
	if elapsed > limit {
		return ErrExceeded{
			Kind:  "time",
			Usage: elapsed.String(),
			Limit: limit.String(),
		}
	}
	return nil
}

// Usage returns the accumulated metrics.
func (m *Monitor) Usage() (cost float64, tokens int64, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.costUsed, m.tokensUsed, time.Since(m.startTime)
}

// Config returns a clone of the underlying budget config.
func (m *Monitor) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Clone()
}
