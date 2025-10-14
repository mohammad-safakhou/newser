package telemetry

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/mohammad-safakhou/newser/internal/budget"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

const (
	budgetAlertThresholdRatio = 0.8
	budgetAlertCooldown       = time.Minute
)

// Telemetry provides comprehensive monitoring and cost tracking
type Telemetry struct {
	config      config.TelemetryConfig
	logger      *log.Logger
	metrics     *Metrics
	costTracker *CostTracker
	mu          sync.RWMutex
	// otel instruments
	runDuration           otelmetric.Float64Histogram
	runCost               otelmetric.Float64Histogram
	runTokens             otelmetric.Int64Counter
	runFailures           otelmetric.Int64Counter
	runBudgetUsageCost    otelmetric.Float64Histogram
	runBudgetUsageTokens  otelmetric.Int64Counter
	runBudgetUsageSeconds otelmetric.Float64Histogram
	runBudgetOverruns     otelmetric.Int64Counter
	runBudgetUsageRatio   otelmetric.Float64Histogram
	runBudgetAlerts       otelmetric.Int64Counter
	agentDuration         otelmetric.Float64Histogram
	agentCost             otelmetric.Float64Histogram
	agentTokens           otelmetric.Int64Counter
	agentFailures         otelmetric.Int64Counter
	budgetAlertAt         map[string]time.Time
}

// Metrics holds various performance metrics
type Metrics struct {
	mu sync.RWMutex
	// Processing metrics
	TotalRequests         int64
	SuccessfulRequests    int64
	FailedRequests        int64
	AverageProcessingTime time.Duration

	// Agent metrics
	AgentExecutions   map[string]int64
	AgentSuccessRates map[string]float64
	AgentAverageTimes map[string]time.Duration

	// LLM metrics
	LLMRequests       map[string]int64
	LLMTokensUsed     map[string]int64
	LLMAverageLatency map[string]time.Duration

	// Source metrics
	SourceRequests     map[string]int64
	SourceSuccessRates map[string]float64
	SourceAverageTimes map[string]time.Duration

	// Research metrics
	ResearchRuns               int64
	ResearchPagesFetchedTotal  int64
	ResearchSourcesTotal       int64
	ResearchUniqueDomainsTotal int64

	// Analysis metrics
	AnalysisRuns                   int64
	AnalysisSourcesConsideredTotal int64
	AnalysisMinCredibilityTotal    float64

	// Knowledge graph metrics
	KGRuns       int64
	KGNodesTotal int64
	KGEdgesTotal int64

	// Conflict detection metrics
	ConflictRuns                 int64
	ConflictCountTotal           int64
	ContradictoryThresholdsTotal float64
}

// CostTracker tracks costs across different LLM providers and operations
type CostTracker struct {
	mu sync.RWMutex
	// Daily costs
	DailyCosts map[string]float64 // provider -> cost

	// Operation costs
	OperationCosts map[string]float64 // operation -> cost

	// Model costs
	ModelCosts map[string]float64 // model -> cost

	// Total costs
	TotalCost   float64
	TotalTokens int64
}

// ProcessingEvent represents a single processing event
type ProcessingEvent struct {
	ID             string
	UserThought    string
	StartTime      time.Time
	EndTime        time.Time
	ProcessingTime time.Duration
	Success        bool
	Error          string
	Cost           float64
	TokensUsed     int64
	AgentsUsed     []string
	SourcesUsed    []string
	LLMModelsUsed  []string
}

// AgentEvent represents an agent execution event
type AgentEvent struct {
	ID         string
	AgentType  string
	StartTime  time.Time
	EndTime    time.Time
	Duration   time.Duration
	Success    bool
	Error      string
	Cost       float64
	TokensUsed int64
	ModelUsed  string
	Confidence float64
}

// SourceEvent represents a source access event
type SourceEvent struct {
	ID        string
	Source    string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Success   bool
	Error     string
	Results   int
}

// NewTelemetry creates a new telemetry instance
func NewTelemetry(config config.TelemetryConfig) *Telemetry {
	t := &Telemetry{
		config: config,
		logger: log.New(log.Writer(), "[TELEMETRY] ", log.LstdFlags),
		metrics: &Metrics{
			AgentExecutions:    make(map[string]int64),
			AgentSuccessRates:  make(map[string]float64),
			AgentAverageTimes:  make(map[string]time.Duration),
			LLMRequests:        make(map[string]int64),
			LLMTokensUsed:      make(map[string]int64),
			LLMAverageLatency:  make(map[string]time.Duration),
			SourceRequests:     make(map[string]int64),
			SourceSuccessRates: make(map[string]float64),
			SourceAverageTimes: make(map[string]time.Duration),
		},
		costTracker: &CostTracker{
			DailyCosts:     make(map[string]float64),
			OperationCosts: make(map[string]float64),
			ModelCosts:     make(map[string]float64),
		},
		budgetAlertAt: make(map[string]time.Time),
	}

	meter := otel.Meter("newser/internal/agent")
	if hist, err := meter.Float64Histogram(
		"agent_run_duration_seconds",
		otelmetric.WithDescription("Duration of end-to-end runs"),
		otelmetric.WithUnit("s"),
	); err != nil {
		t.logger.Printf("otel histogram agent_run_duration_seconds: %v", err)
	} else {
		t.runDuration = hist
	}
	if hist, err := meter.Float64Histogram(
		"agent_run_cost_usd",
		otelmetric.WithDescription("Cost of end-to-end runs in USD"),
		otelmetric.WithUnit("USD"),
	); err != nil {
		t.logger.Printf("otel histogram agent_run_cost_usd: %v", err)
	} else {
		t.runCost = hist
	}
	if ctr, err := meter.Int64Counter(
		"agent_run_tokens",
		otelmetric.WithDescription("Tokens consumed per run"),
	); err != nil {
		t.logger.Printf("otel counter agent_run_tokens: %v", err)
	} else {
		t.runTokens = ctr
	}
	if ctr, err := meter.Int64Counter(
		"agent_run_failures",
		otelmetric.WithDescription("Count of failed runs"),
	); err != nil {
		t.logger.Printf("otel counter agent_run_failures: %v", err)
	} else {
		t.runFailures = ctr
	}
	if hist, err := meter.Float64Histogram(
		"agent_run_budget_cost_usd",
		otelmetric.WithDescription("Budget usage per run (USD)"),
		otelmetric.WithUnit("USD"),
	); err != nil {
		t.logger.Printf("otel histogram agent_run_budget_cost_usd: %v", err)
	} else {
		t.runBudgetUsageCost = hist
	}
	if ctr, err := meter.Int64Counter(
		"agent_run_budget_tokens",
		otelmetric.WithDescription("Budget tokens consumed per run"),
	); err != nil {
		t.logger.Printf("otel counter agent_run_budget_tokens: %v", err)
	} else {
		t.runBudgetUsageTokens = ctr
	}
	if hist, err := meter.Float64Histogram(
		"agent_run_budget_time_seconds",
		otelmetric.WithDescription("Budget elapsed time per run"),
		otelmetric.WithUnit("s"),
	); err != nil {
		t.logger.Printf("otel histogram agent_run_budget_time_seconds: %v", err)
	} else {
		t.runBudgetUsageSeconds = hist
	}
	if ctr, err := meter.Int64Counter(
		"agent_run_budget_overruns",
		otelmetric.WithDescription("Count of runs exceeding budget"),
	); err != nil {
		t.logger.Printf("otel counter agent_run_budget_overruns: %v", err)
	} else {
		t.runBudgetOverruns = ctr
	}
	if hist, err := meter.Float64Histogram(
		"agent_run_budget_usage_ratio",
		otelmetric.WithDescription("Ratio of budget consumption to configured limit"),
		otelmetric.WithUnit("1"),
	); err != nil {
		t.logger.Printf("otel histogram agent_run_budget_usage_ratio: %v", err)
	} else {
		t.runBudgetUsageRatio = hist
	}
	if ctr, err := meter.Int64Counter(
		"agent_run_budget_alerts",
		otelmetric.WithDescription("Budget watchdog alerts when usage nears limits"),
	); err != nil {
		t.logger.Printf("otel counter agent_run_budget_alerts: %v", err)
	} else {
		t.runBudgetAlerts = ctr
	}
	if hist, err := meter.Float64Histogram(
		"agent_execution_duration_seconds",
		otelmetric.WithDescription("Duration of individual agent executions"),
		otelmetric.WithUnit("s"),
	); err != nil {
		t.logger.Printf("otel histogram agent_execution_duration_seconds: %v", err)
	} else {
		t.agentDuration = hist
	}
	if hist, err := meter.Float64Histogram(
		"agent_execution_cost_usd",
		otelmetric.WithDescription("Cost per agent execution in USD"),
		otelmetric.WithUnit("USD"),
	); err != nil {
		t.logger.Printf("otel histogram agent_execution_cost_usd: %v", err)
	} else {
		t.agentCost = hist
	}
	if ctr, err := meter.Int64Counter(
		"agent_execution_tokens",
		otelmetric.WithDescription("Tokens consumed per agent execution"),
	); err != nil {
		t.logger.Printf("otel counter agent_execution_tokens: %v", err)
	} else {
		t.agentTokens = ctr
	}
	if ctr, err := meter.Int64Counter(
		"agent_execution_failures",
		otelmetric.WithDescription("Count of failed agent executions"),
	); err != nil {
		t.logger.Printf("otel counter agent_execution_failures: %v", err)
	} else {
		t.agentFailures = ctr
	}

	// Start background tasks (periodic logs can be disabled via config)
	if config.Enabled && config.PeriodicLogs {
		go t.startMetricsCollection()
		go t.startCostReporting()
	}

	return t
}

// RecordProcessingEvent records a complete processing event
func (t *Telemetry) RecordProcessingEvent(ctx context.Context, event ProcessingEvent) {
	if !t.config.Enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	attrs := []attribute.KeyValue{
		attribute.String("outcome", outcomeLabel(event.Success)),
	}
	if t.runDuration != nil && event.ProcessingTime > 0 {
		t.runDuration.Record(ctx, event.ProcessingTime.Seconds(), otelmetric.WithAttributes(attrs...))
	}
	if t.runCost != nil && event.Cost > 0 {
		t.runCost.Record(ctx, event.Cost, otelmetric.WithAttributes(attrs...))
	}
	if t.runTokens != nil && event.TokensUsed > 0 {
		t.runTokens.Add(ctx, event.TokensUsed, otelmetric.WithAttributes(attrs...))
	}
	if !event.Success && t.runFailures != nil {
		t.runFailures.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
	}

	// Update metrics
	t.metrics.TotalRequests++
	if event.Success {
		t.metrics.SuccessfulRequests++
	} else {
		t.metrics.FailedRequests++
	}

	// Update average processing time
	if t.metrics.TotalRequests == 1 {
		t.metrics.AverageProcessingTime = event.ProcessingTime
	} else {
		total := t.metrics.AverageProcessingTime * time.Duration(t.metrics.TotalRequests-1)
		t.metrics.AverageProcessingTime = (total + event.ProcessingTime) / time.Duration(t.metrics.TotalRequests)
	}

	// Update agent metrics
	for _, agent := range event.AgentsUsed {
		t.metrics.AgentExecutions[agent]++
	}

	// Update LLM metrics
	for _, model := range event.LLMModelsUsed {
		t.metrics.LLMRequests[model]++
		t.metrics.LLMTokensUsed[model] += event.TokensUsed
	}

	// Update source metrics
	for _, source := range event.SourcesUsed {
		t.metrics.SourceRequests[source]++
	}

	// Update cost tracking
	t.costTracker.TotalCost += event.Cost
	t.costTracker.TotalTokens += event.TokensUsed

	// Log the event
	t.logger.Printf("Processing Event: ID=%s, Success=%t, Duration=%v, Cost=$%.4f, Tokens=%d",
		event.ID, event.Success, event.ProcessingTime, event.Cost, event.TokensUsed)
}

// RecordBudgetUsage emits telemetry for budget consumption and overruns.
func (t *Telemetry) RecordBudgetUsage(ctx context.Context, usage budget.Usage, cfg budget.Config, breached bool) {
	if !t.config.Enabled {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.Bool("budget.require_approval", cfg.RequireApproval),
	}
	makeAttrs := func(extra ...attribute.KeyValue) []attribute.KeyValue {
		out := make([]attribute.KeyValue, 0, len(attrs)+len(extra))
		out = append(out, attrs...)
		out = append(out, extra...)
		return out
	}
	if cfg.MaxCost != nil {
		attrs = append(attrs, attribute.Float64("budget.max_cost", *cfg.MaxCost))
	}
	if cfg.MaxTokens != nil {
		attrs = append(attrs, attribute.Int64("budget.max_tokens", *cfg.MaxTokens))
	}
	if cfg.MaxTimeSeconds != nil {
		attrs = append(attrs, attribute.Int64("budget.max_time_seconds", *cfg.MaxTimeSeconds))
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.runBudgetUsageCost != nil && usage.Cost > 0 {
		t.runBudgetUsageCost.Record(ctx, usage.Cost, otelmetric.WithAttributes(attrs...))
	}
	if t.runBudgetUsageTokens != nil && usage.Tokens > 0 {
		t.runBudgetUsageTokens.Add(ctx, usage.Tokens, otelmetric.WithAttributes(attrs...))
	}
	if t.runBudgetUsageSeconds != nil && usage.Elapsed > 0 {
		t.runBudgetUsageSeconds.Record(ctx, usage.Elapsed.Seconds(), otelmetric.WithAttributes(attrs...))
	}
	if breached && t.runBudgetOverruns != nil {
		t.runBudgetOverruns.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
	}

	recordRatio := func(dimension string, used, limit float64) {
		if limit <= 0 || used <= 0 {
			return
		}
		ratio := used / limit
		dimAttrs := makeAttrs(attribute.String("budget.dimension", dimension))
		if t.runBudgetUsageRatio != nil {
			t.runBudgetUsageRatio.Record(ctx, ratio, otelmetric.WithAttributes(dimAttrs...))
		}
		if breached {
			return
		}
		if ratio < budgetAlertThresholdRatio {
			return
		}
		alertAttrs := makeAttrs(
			attribute.String("budget.dimension", dimension),
			attribute.Bool("budget.alert", true),
		)
		if t.runBudgetAlerts != nil {
			t.runBudgetAlerts.Add(ctx, 1, otelmetric.WithAttributes(alertAttrs...))
		}
		now := time.Now()
		last := t.budgetAlertAt[dimension]
		if last.IsZero() || now.Sub(last) >= budgetAlertCooldown {
			t.budgetAlertAt[dimension] = now
			t.logger.Printf("Budget watchdog alert: %s usage at %.1f%% of limit", dimension, ratio*100)
		}
	}

	if cfg.MaxCost != nil && *cfg.MaxCost > 0 && usage.Cost > 0 {
		recordRatio("cost", usage.Cost, *cfg.MaxCost)
	}
	if cfg.MaxTokens != nil && *cfg.MaxTokens > 0 && usage.Tokens > 0 {
		recordRatio("tokens", float64(usage.Tokens), float64(*cfg.MaxTokens))
	}
	if cfg.MaxTimeSeconds != nil && *cfg.MaxTimeSeconds > 0 && usage.Elapsed > 0 {
		recordRatio("time", usage.Elapsed.Seconds(), float64(*cfg.MaxTimeSeconds))
	}
}

// RecordAgentEvent records an agent execution event
func (t *Telemetry) RecordAgentEvent(ctx context.Context, event AgentEvent) {
	if !t.config.Enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	attrs := []attribute.KeyValue{
		attribute.String("agent_type", event.AgentType),
		attribute.String("model", event.ModelUsed),
		attribute.String("outcome", outcomeLabel(event.Success)),
	}
	if t.agentDuration != nil && event.Duration > 0 {
		t.agentDuration.Record(ctx, event.Duration.Seconds(), otelmetric.WithAttributes(attrs...))
	}
	if t.agentCost != nil && event.Cost > 0 {
		t.agentCost.Record(ctx, event.Cost, otelmetric.WithAttributes(attrs...))
	}
	if t.agentTokens != nil && event.TokensUsed > 0 {
		t.agentTokens.Add(ctx, event.TokensUsed, otelmetric.WithAttributes(attrs...))
	}
	if !event.Success && t.agentFailures != nil {
		t.agentFailures.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
	}

	// Update agent metrics
	t.metrics.AgentExecutions[event.AgentType]++

	// Update success rate
	currentSuccess := t.metrics.AgentSuccessRates[event.AgentType]
	currentExecutions := t.metrics.AgentExecutions[event.AgentType]
	if event.Success {
		currentSuccess += 1.0
	}
	t.metrics.AgentSuccessRates[event.AgentType] = currentSuccess / float64(currentExecutions)

	// Update average time
	currentAvg := t.metrics.AgentAverageTimes[event.AgentType]
	if currentExecutions == 1 {
		t.metrics.AgentAverageTimes[event.AgentType] = event.Duration
	} else {
		total := currentAvg * time.Duration(currentExecutions-1)
		t.metrics.AgentAverageTimes[event.AgentType] = (total + event.Duration) / time.Duration(currentExecutions)
	}

	// Update LLM metrics
	t.metrics.LLMRequests[event.ModelUsed]++
	t.metrics.LLMTokensUsed[event.ModelUsed] += event.TokensUsed

	// Update cost tracking
	t.costTracker.TotalCost += event.Cost
	t.costTracker.TotalTokens += event.TokensUsed
	t.costTracker.ModelCosts[event.ModelUsed] += event.Cost

	t.logger.Printf("Agent Event: Type=%s, Success=%t, Duration=%v, Cost=$%.4f, Confidence=%.2f",
		event.AgentType, event.Success, event.Duration, event.Cost, event.Confidence)
}

// RecordSourceEvent records a source access event
func (t *Telemetry) RecordSourceEvent(ctx context.Context, event SourceEvent) {
	if !t.config.Enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Update source metrics
	t.metrics.SourceRequests[event.Source]++

	// Update success rate
	currentSuccess := t.metrics.SourceSuccessRates[event.Source]
	currentRequests := t.metrics.SourceRequests[event.Source]
	if event.Success {
		currentSuccess += 1.0
	}
	t.metrics.SourceSuccessRates[event.Source] = currentSuccess / float64(currentRequests)

	// Update average time
	currentAvg := t.metrics.SourceAverageTimes[event.Source]
	if currentRequests == 1 {
		t.metrics.SourceAverageTimes[event.Source] = event.Duration
	} else {
		total := currentAvg * time.Duration(currentRequests-1)
		t.metrics.SourceAverageTimes[event.Source] = (total + event.Duration) / time.Duration(currentRequests)
	}

	t.logger.Printf("Source Event: Source=%s, Success=%t, Duration=%v, Results=%d",
		event.Source, event.Success, event.Duration, event.Results)
}

// RecordResearchStats records research pagination/diversity stats
func (t *Telemetry) RecordResearchStats(ctx context.Context, pagesFetched, totalSources, uniqueDomains int) {
	if !t.config.Enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics.ResearchRuns++
	t.metrics.ResearchPagesFetchedTotal += int64(pagesFetched)
	t.metrics.ResearchSourcesTotal += int64(totalSources)
	t.metrics.ResearchUniqueDomainsTotal += int64(uniqueDomains)
}

// RecordAnalysisStats records analysis parameter usage
func (t *Telemetry) RecordAnalysisStats(ctx context.Context, sourcesConsidered int, minCredibility float64) {
	if !t.config.Enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics.AnalysisRuns++
	t.metrics.AnalysisSourcesConsideredTotal += int64(sourcesConsidered)
	t.metrics.AnalysisMinCredibilityTotal += minCredibility
}

// RecordKGStats records knowledge graph sizes
func (t *Telemetry) RecordKGStats(ctx context.Context, nodes, edges int) {
	if !t.config.Enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics.KGRuns++
	t.metrics.KGNodesTotal += int64(nodes)
	t.metrics.KGEdgesTotal += int64(edges)
}

// RecordConflictStats records conflict detection outputs
func (t *Telemetry) RecordConflictStats(ctx context.Context, conflicts int, contradictoryThreshold float64) {
	if !t.config.Enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics.ConflictRuns++
	t.metrics.ConflictCountTotal += int64(conflicts)
	t.metrics.ContradictoryThresholdsTotal += contradictoryThreshold
}

func outcomeLabel(success bool) string {
	if success {
		return "success"
	}
	return "failure"
}

// GetMetrics returns current metrics snapshot
func (t *Telemetry) GetMetrics() Metrics {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	metrics := *t.metrics
	metrics.AgentExecutions = make(map[string]int64)
	metrics.AgentSuccessRates = make(map[string]float64)
	metrics.AgentAverageTimes = make(map[string]time.Duration)
	metrics.LLMRequests = make(map[string]int64)
	metrics.LLMTokensUsed = make(map[string]int64)
	metrics.LLMAverageLatency = make(map[string]time.Duration)
	metrics.SourceRequests = make(map[string]int64)
	metrics.SourceSuccessRates = make(map[string]float64)
	metrics.SourceAverageTimes = make(map[string]time.Duration)

	for k, v := range t.metrics.AgentExecutions {
		metrics.AgentExecutions[k] = v
	}
	for k, v := range t.metrics.AgentSuccessRates {
		metrics.AgentSuccessRates[k] = v
	}
	for k, v := range t.metrics.AgentAverageTimes {
		metrics.AgentAverageTimes[k] = v
	}
	for k, v := range t.metrics.LLMRequests {
		metrics.LLMRequests[k] = v
	}
	for k, v := range t.metrics.LLMTokensUsed {
		metrics.LLMTokensUsed[k] = v
	}
	for k, v := range t.metrics.LLMAverageLatency {
		metrics.LLMAverageLatency[k] = v
	}
	for k, v := range t.metrics.SourceRequests {
		metrics.SourceRequests[k] = v
	}
	for k, v := range t.metrics.SourceSuccessRates {
		metrics.SourceSuccessRates[k] = v
	}
	for k, v := range t.metrics.SourceAverageTimes {
		metrics.SourceAverageTimes[k] = v
	}

	return metrics
}

// GetCostSummary returns current cost summary
func (t *Telemetry) GetCostSummary() CostSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	summary := CostSummary{
		TotalCost:      t.costTracker.TotalCost,
		TotalTokens:    t.costTracker.TotalTokens,
		DailyCosts:     make(map[string]float64),
		ModelCosts:     make(map[string]float64),
		OperationCosts: make(map[string]float64),
	}

	for k, v := range t.costTracker.DailyCosts {
		summary.DailyCosts[k] = v
	}
	for k, v := range t.costTracker.ModelCosts {
		summary.ModelCosts[k] = v
	}
	for k, v := range t.costTracker.OperationCosts {
		summary.OperationCosts[k] = v
	}

	return summary
}

// CostSummary provides a summary of costs
type CostSummary struct {
	TotalCost      float64
	TotalTokens    int64
	DailyCosts     map[string]float64
	ModelCosts     map[string]float64
	OperationCosts map[string]float64
}

// startMetricsCollection starts periodic metrics collection
func (t *Telemetry) startMetricsCollection() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		metrics := t.GetMetrics()
		costs := t.GetCostSummary()

		t.logger.Printf("Metrics Snapshot: Requests=%d/%d, AvgTime=%v, TotalCost=$%.4f, TotalTokens=%d",
			metrics.SuccessfulRequests, metrics.TotalRequests,
			metrics.AverageProcessingTime, costs.TotalCost, costs.TotalTokens)
	}
}

// startCostReporting starts periodic cost reporting
func (t *Telemetry) startCostReporting() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		costs := t.GetCostSummary()

		t.logger.Printf("Cost Report: Total=$%.4f, Tokens=%d", costs.TotalCost, costs.TotalTokens)

		// Log model costs
		for model, cost := range costs.ModelCosts {
			t.logger.Printf("  Model %s: $%.4f", model, cost)
		}

		// Log operation costs
		for op, cost := range costs.OperationCosts {
			t.logger.Printf("  Operation %s: $%.4f", op, cost)
		}
	}
}

// Shutdown gracefully shuts down the telemetry system
func (t *Telemetry) Shutdown() {
	t.logger.Println("Shutting down telemetry system...")

	// Final metrics report
	metrics := t.GetMetrics()
	costs := t.GetCostSummary()

	t.logger.Printf("Final Report:")
	t.logger.Printf("  Total Requests: %d", metrics.TotalRequests)
	t.logger.Printf("  Success Rate: %.2f%%", float64(metrics.SuccessfulRequests)/float64(metrics.TotalRequests)*100)
	t.logger.Printf("  Average Processing Time: %v", metrics.AverageProcessingTime)
	t.logger.Printf("  Total Cost: $%.4f", costs.TotalCost)
	t.logger.Printf("  Total Tokens: %d", costs.TotalTokens)
}

// CalculateCost calculates the cost for a given number of tokens and model
func (t *Telemetry) CalculateCost(inputTokens, outputTokens int64, modelName string, costPer1KInput, costPer1KOutput float64) float64 {
	inputCost := float64(inputTokens) / 1000.0 * costPer1KInput
	outputCost := float64(outputTokens) / 1000.0 * costPer1KOutput
	return inputCost + outputCost
}

// GetPerformanceReport returns a detailed performance report
func (t *Telemetry) GetPerformanceReport() string {
	metrics := t.GetMetrics()
	costs := t.GetCostSummary()

	report := fmt.Sprintf(`
=== PERFORMANCE REPORT ===
Overall Metrics:
  Total Requests: %d
  Successful: %d (%.2f%%)
  Failed: %d (%.2f%%)
  Average Processing Time: %v
  Total Cost: $%.4f
  Total Tokens: %d

Agent Performance:
`, metrics.TotalRequests, metrics.SuccessfulRequests,
		float64(metrics.SuccessfulRequests)/float64(metrics.TotalRequests)*100,
		metrics.FailedRequests, float64(metrics.FailedRequests)/float64(metrics.TotalRequests)*100,
		metrics.AverageProcessingTime, costs.TotalCost, costs.TotalTokens)

	for agent, executions := range metrics.AgentExecutions {
		successRate := metrics.AgentSuccessRates[agent]
		avgTime := metrics.AgentAverageTimes[agent]
		report += fmt.Sprintf("  %s: %d executions, %.2f%% success, %v avg time\n",
			agent, executions, successRate*100, avgTime)
	}

	report += "\nLLM Usage:\n"
	for model, requests := range metrics.LLMRequests {
		tokens := metrics.LLMTokensUsed[model]
		cost := costs.ModelCosts[model]
		report += fmt.Sprintf("  %s: %d requests, %d tokens, $%.4f\n",
			model, requests, tokens, cost)
	}

	report += "\nSource Performance:\n"
	for source, requests := range metrics.SourceRequests {
		successRate := metrics.SourceSuccessRates[source]
		avgTime := metrics.SourceAverageTimes[source]
		report += fmt.Sprintf("  %s: %d requests, %.2f%% success, %v avg time\n",
			source, requests, successRate*100, avgTime)
	}

	return report
}
