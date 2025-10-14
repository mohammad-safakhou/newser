package streams

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

var (
	streamMetricsOnce    sync.Once
	crawlerRequests      otelmetric.Int64Counter
	crawlerRequestedURLs otelmetric.Int64Counter
	crawlerPolicyBudget  otelmetric.Float64Histogram
	crawlerDedupRatio    otelmetric.Float64Histogram
)

func initStreamMetrics() {
	meter := otel.Meter("newser/queue/streams")
	var err error
	crawlerRequests, err = meter.Int64Counter(
		"crawler_requests_total",
		otelmetric.WithDescription("Crawler fetch requests published to streams"),
	)
	if err != nil {
		log.Printf("queue streams metrics init: crawler_requests_total: %v", err)
	}
	crawlerRequestedURLs, err = meter.Int64Counter(
		"crawler_requested_urls_total",
		otelmetric.WithDescription("Total URLs requested by crawler events"),
	)
	if err != nil {
		log.Printf("queue streams metrics init: crawler_requested_urls_total: %v", err)
	}
	crawlerPolicyBudget, err = meter.Float64Histogram(
		"crawler_policy_budget_seconds",
		otelmetric.WithDescription("Politeness budget requested for crawl executions"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		log.Printf("queue streams metrics init: crawler_policy_budget_seconds: %v", err)
	}
	crawlerDedupRatio, err = meter.Float64Histogram(
		"crawler_dedup_ratio",
		otelmetric.WithDescription("Reported deduplication ratio from crawler events"),
	)
	if err != nil {
		log.Printf("queue streams metrics init: crawler_dedup_ratio: %v", err)
	}
}

func recordStreamMetrics(ctx context.Context, eventType string, payload []byte) {
	switch eventType {
	case "crawl.request":
		streamMetricsOnce.Do(initStreamMetrics)
		if crawlerRequests == nil {
			return
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(payload, &doc); err != nil {
			return
		}
		profile, _ := doc["policy_profile"].(string)
		attrs := []attribute.KeyValue{
			attribute.String("policy_profile", strings.TrimSpace(profile)),
		}
		crawlerRequests.Add(contextOrBackground(ctx), 1, otelmetric.WithAttributes(attrs...))

		if arr, ok := doc["urls"].([]interface{}); ok && crawlerRequestedURLs != nil {
			crawlerRequestedURLs.Add(contextOrBackground(ctx), int64(len(arr)), otelmetric.WithAttributes(attrs...))
		}
		if v, ok := doc["politeness_budget_seconds"].(float64); ok && crawlerPolicyBudget != nil && v > 0 {
			crawlerPolicyBudget.Record(contextOrBackground(ctx), v, otelmetric.WithAttributes(attrs...))
		}
		if ratio, ok := doc["dedup_ratio"].(float64); ok && crawlerDedupRatio != nil {
			crawlerDedupRatio.Record(contextOrBackground(ctx), ratio, otelmetric.WithAttributes(attrs...))
		}
	}
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
