package core

import (
	"testing"
)

func TestBuildEvidenceIncludesSources(t *testing.T) {
	items := []map[string]interface{}{
		{
			"id":         "ev-1",
			"summary":    "Claim text",
			"source_ids": []interface{}{"src-1"},
			"sources": []interface{}{
				map[string]interface{}{
					"id":      "src-1",
					"url":     "https://example.com/article",
					"title":   "Example",
					"snippet": "Snippet from article",
					"domain":  "example.com",
				},
			},
		},
	}
	lookup := map[string]*Source{
		"src-1": {
			ID:          "src-1",
			Title:       "Example",
			URL:         "https://example.com/article",
			Credibility: 0.8,
			Summary:     "Snippet from article",
		},
	}

	evidence := buildEvidence(items, lookup)
	if len(evidence) != 1 {
		t.Fatalf("expected evidence entry")
	}
	ev := evidence[0]
	if len(ev.Sources) != 1 {
		t.Fatalf("expected evidence sources")
	}
	src := ev.Sources[0]
	if src.ID != "src-1" {
		t.Fatalf("unexpected source id: %s", src.ID)
	}
	if src.Credibility != 0.8 {
		t.Fatalf("unexpected credibility: %.2f", src.Credibility)
	}
	if src.Snippet == "" {
		t.Fatalf("expected snippet to be populated")
	}
}
