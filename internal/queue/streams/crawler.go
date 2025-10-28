package streams

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mohammad-safakhou/newser/internal/helpers"
)

const (
	// StreamCrawlerSchedule is the fan-out stream that schedules URLs to shard queues.
	StreamCrawlerSchedule = "crawl.schedule"
	// StreamCrawlerShardPrefix prefixes per-shard streams consumed by crawler workers.
	StreamCrawlerShardPrefix = "crawl.shard"
	// StreamCrawlerDedup carries canonicalisation/deduplication results.
	StreamCrawlerDedup = "crawl.dedup"
	// StreamCrawlerRefresh emits refresh cadence updates per topic.
	StreamCrawlerRefresh = "crawl.refresh"
)

// CrawlerTopology captures crawler stream naming and shard behaviour.
type CrawlerTopology struct {
	shardCount int
}

// NewCrawlerTopology constructs a topology with the provided shard count.
func NewCrawlerTopology(shardCount int) CrawlerTopology {
	if shardCount <= 0 {
		shardCount = 1
	}
	return CrawlerTopology{shardCount: shardCount}
}

// ShardCount returns the configured shard count.
func (c CrawlerTopology) ShardCount() int {
	return c.shardCount
}

// ShardIndex computes the shard number for the provided fingerprint.
func (c CrawlerTopology) ShardIndex(fingerprint string) int {
	if c.shardCount <= 1 {
		return 0
	}
	trim := strings.TrimSpace(fingerprint)
	if len(trim) >= 16 {
		trim = trim[:16]
	}
	val, err := strconv.ParseUint(trim, 16, 64)
	if err != nil {
		// Fallback to string hash when fingerprint is malformed.
		var sum uint64
		for i := 0; i < len(fingerprint); i++ {
			sum = sum*33 + uint64(fingerprint[i])
		}
		val = sum
	}
	return int(val % uint64(c.shardCount))
}

// ShardStream returns the Redis stream name for the shard index.
func (c CrawlerTopology) ShardStream(index int) string {
	if c.shardCount <= 1 {
		return fmt.Sprintf("%s.00", StreamCrawlerShardPrefix)
	}
	idx := index % c.shardCount
	if idx < 0 {
		idx = (idx + c.shardCount) % c.shardCount
	}
	return fmt.Sprintf("%s.%02d", StreamCrawlerShardPrefix, idx)
}

// PartitionURL canonicalises the URL, derives the fingerprint, and selects the shard stream.
func (c CrawlerTopology) PartitionURL(rawURL string) (canonical string, fingerprint string, shard int, stream string, err error) {
	canonical, err = helpers.CanonicalURL(rawURL)
	if err != nil {
		return "", "", 0, "", err
	}
	fingerprint, err = helpers.URLFingerprint(canonical)
	if err != nil {
		return "", "", 0, "", err
	}
	shard = c.ShardIndex(fingerprint)
	stream = c.ShardStream(shard)
	return
}

// DedupStream returns the deduplication stream name for the given topic.
func (c CrawlerTopology) DedupStream(topicID string) string {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return StreamCrawlerDedup
	}
	return fmt.Sprintf("%s.%s", StreamCrawlerDedup, topicID)
}

// RefreshStream returns the refresh cadence stream for the topic.
func (c CrawlerTopology) RefreshStream(topicID string) string {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return StreamCrawlerRefresh
	}
	return fmt.Sprintf("%s.%s", StreamCrawlerRefresh, topicID)
}

// SchedulePayload represents an entry published to the crawl.schedule stream.
type SchedulePayload struct {
	TopicID             string                 `json:"topic_id"`
	OriginalURL         string                 `json:"original_url,omitempty"`
	CanonicalURL        string                 `json:"canonical_url"`
	Fingerprint         string                 `json:"fingerprint"`
	Shard               int                    `json:"shard"`
	RefreshAfterSeconds int64                  `json:"refresh_after_seconds"`
	PolicyProfile       string                 `json:"policy_profile,omitempty"`
	Priority            int                    `json:"priority,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}

// DedupPayload captures the output of canonicalisation/deduplication.
type DedupPayload struct {
	TopicID             string                 `json:"topic_id"`
	OriginalURL         string                 `json:"original_url"`
	CanonicalURL        string                 `json:"canonical_url"`
	Fingerprint         string                 `json:"fingerprint"`
	Shard               int                    `json:"shard"`
	ContentHash         string                 `json:"content_hash,omitempty"`
	Duplicate           bool                   `json:"duplicate"`
	MatchedRunID        string                 `json:"matched_run_id,omitempty"`
	MatchedFingerprint  string                 `json:"matched_fingerprint,omitempty"`
	DedupRatio          float64                `json:"dedup_ratio,omitempty"`
	RefreshAfterSeconds int64                  `json:"refresh_after_seconds,omitempty"`
	CheckedAt           string                 `json:"checked_at"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}
