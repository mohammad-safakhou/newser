package streams

import "testing"

func TestCrawlerTopologyPartition(t *testing.T) {
	topo := NewCrawlerTopology(8)
	canonical, fingerprint, shard, stream, err := topo.PartitionURL("https://Example.com/path?a=1&utm_source=rss")
	if err != nil {
		t.Fatalf("PartitionURL error: %v", err)
	}
	if canonical != "https://example.com/path?a=1" {
		t.Fatalf("unexpected canonical url: %s", canonical)
	}
	if len(fingerprint) != 64 {
		t.Fatalf("expected 64 char fingerprint, got %d", len(fingerprint))
	}
	if shard < 0 || shard >= topo.ShardCount() {
		t.Fatalf("shard out of range: %d", shard)
	}
	if stream == "" {
		t.Fatalf("expected shard stream")
	}
}

func TestCrawlerTopologyStreams(t *testing.T) {
	topo := NewCrawlerTopology(4)
	if topo.DedupStream("") != StreamCrawlerDedup {
		t.Fatalf("expected global dedup stream")
	}
	if got := topo.DedupStream("topic-1"); got != "crawl.dedup.topic-1" {
		t.Fatalf("unexpected dedup stream: %s", got)
	}
	if got := topo.RefreshStream("topic-1"); got != "crawl.refresh.topic-1" {
		t.Fatalf("unexpected refresh stream: %s", got)
	}
}
