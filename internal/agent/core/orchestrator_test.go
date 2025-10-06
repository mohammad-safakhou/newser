package core

import (
	"testing"
)

func TestNormalizeItemSourcesAddsIDsAndSnippets(t *testing.T) {
	sources := []Source{
		{ID: "src-1", URL: "https://example.com/news/a", Summary: "First summary"},
		{ID: "src-2", URL: "https://en.wikipedia.org/wiki/Example", Summary: ""},
	}
	lookup := buildSourceLookup(sources)
	item := map[string]interface{}{
		"title":   "Example claim",
		"sources": []interface{}{map[string]interface{}{"url": "https://example.com/news/a"}, map[string]interface{}{"url": "https://en.wikipedia.org/wiki/Example"}},
	}

	norm, ids := normalizeItemSources(item, lookup)
	if len(norm) != 2 {
		t.Fatalf("expected 2 normalized sources, got %d", len(norm))
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 source ids, got %d", len(ids))
	}
	if norm[0]["id"] != "src-1" {
		t.Fatalf("expected first source id src-1, got %v", norm[0]["id"])
	}
	if snippet, ok := norm[0]["snippet"].(string); !ok || snippet == "" {
		t.Fatalf("expected snippet for primary source, got %v", norm[0]["snippet"])
	}
	if _, ok := norm[1]["snippet"]; ok {
		t.Fatalf("did not expect snippet for secondary source")
	}
}

func TestNormalizeItemSourcesRejectsUnmatched(t *testing.T) {
	lookup := buildSourceLookup([]Source{{ID: "src-1", URL: "https://example.com/article"}})
	item := map[string]interface{}{
		"title":   "Broken",
		"sources": []interface{}{map[string]interface{}{"url": "https://unknown.com/a"}},
	}
	norm, ids := normalizeItemSources(item, lookup)
	if norm != nil || ids != nil {
		t.Fatalf("expected normalization to fail for unknown source")
	}
}
