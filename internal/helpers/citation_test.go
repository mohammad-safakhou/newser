package helpers

import (
	"testing"
	"time"
)

func TestFormatCitation(t *testing.T) {
	t.Parallel()
	c := Citation{
		SourceID:  "S1",
		Title:     "Investigative Report",
		URL:       "https://example.com/news/report?ref=homepage",
		Snippet:   "Key findings indicate a significant shift in policy direction.",
		Published: time.Date(2024, 4, 15, 0, 0, 0, 0, time.UTC),
	}

	got := FormatCitation(c)
	want := `[S1] Investigative Report — "Key findings indicate a significant shift in policy direction." (example.com, 2024-04-15) <https://example.com/news/report?ref=homepage>`

	if got != want {
		t.Fatalf("FormatCitation() = %q, want %q", got, want)
	}
}

func TestFormatCitationTruncatesSnippet(t *testing.T) {
	t.Parallel()
	c := Citation{
		SourceID: "S2",
		Snippet:  "A very long snippet that should be truncated for neat citation summaries and avoid overly verbose output when rendering footnotes.",
		URL:      "https://example.com/article",
		Accessed: time.Date(2024, 4, 20, 0, 0, 0, 0, time.UTC),
	}

	got := FormatCitation(c, WithMaxSnippetLength(40))
	want := `[S2] — "A very long snippet that should be trunc…" (example.com, retrieved 2024-04-20) <https://example.com/article>`

	if got != want {
		t.Fatalf("FormatCitation() = %q, want %q", got, want)
	}
}

func TestFormatCitationsBatch(t *testing.T) {
	t.Parallel()
	list := []Citation{
		{SourceID: "A", Title: "First", URL: "https://a.example.com"},
		{SourceID: "B", Title: "Second", URL: "https://b.example.com"},
	}
	items := FormatCitations(list)
	if len(items) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(items))
	}
	if items[0] == items[1] {
		t.Fatalf("expected unique entries, got %#v", items)
	}
}
