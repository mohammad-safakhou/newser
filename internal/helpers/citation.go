package helpers

import (
	"net/url"
	"strings"
	"time"
)

// Citation models metadata for a single referenced source.
type Citation struct {
	SourceID   string
	Title      string
	URL        string
	Snippet    string
	Published  time.Time
	Accessed   time.Time
	Attributes map[string]string
}

// citationConfig controls formatting behaviour.
type citationConfig struct {
	maxSnippet int
}

// CitationOption configures citation formatting.
type CitationOption func(*citationConfig)

// WithMaxSnippetLength truncates snippets to the provided length (default 180).
func WithMaxSnippetLength(n int) CitationOption {
	return func(cfg *citationConfig) {
		if n > 0 {
			cfg.maxSnippet = n
		}
	}
}

// FormatCitation renders a single citation string in a consistent layout:
// [sourceID] Title — "Snippet" (Domain, YYYY-MM-DD) <URL>
func FormatCitation(c Citation, opts ...CitationOption) string {
	cfg := citationConfig{maxSnippet: 180}
	for _, opt := range opts {
		opt(&cfg)
	}

	sourceID := strings.TrimSpace(c.SourceID)
	if sourceID == "" {
		sourceID = "source"
	}

	var parts []string
	parts = append(parts, "["+sourceID+"]")

	if title := strings.TrimSpace(c.Title); title != "" {
		parts = append(parts, title)
	}

	if snippet := formatSnippet(c.Snippet, cfg.maxSnippet); snippet != "" {
		parts = append(parts, "— "+snippet)
	}

	if domain := extractDomain(c.URL); domain != "" {
		datePart := ""
		if !c.Published.IsZero() {
			datePart = c.Published.Format("2006-01-02")
		} else if !c.Accessed.IsZero() {
			datePart = "retrieved " + c.Accessed.Format("2006-01-02")
		}
		meta := domain
		if datePart != "" {
			meta = meta + ", " + datePart
		}
		parts = append(parts, "("+meta+")")
	}

	if link := strings.TrimSpace(c.URL); link != "" {
		parts = append(parts, "<"+link+">")
	}

	return strings.Join(parts, " ")
}

// FormatCitations renders a collection of citations.
func FormatCitations(citations []Citation, opts ...CitationOption) []string {
	if len(citations) == 0 {
		return nil
	}
	out := make([]string, 0, len(citations))
	for _, c := range citations {
		out = append(out, FormatCitation(c, opts...))
	}
	return out
}

func formatSnippet(snippet string, limit int) string {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return ""
	}
	// Collapse whitespace.
	snippet = strings.Join(strings.Fields(snippet), " ")
	if limit > 0 && len(snippet) > limit {
		snippet = snippet[:limit]
		if !strings.HasSuffix(snippet, "…") {
			snippet += "…"
		}
	}
	if !strings.HasPrefix(snippet, "\"") {
		snippet = `"` + snippet
	}
	if !strings.HasSuffix(snippet, "\"") {
		snippet = snippet + `"`
	}
	return snippet
}

func extractDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimSuffix(host, ":80")
	host = strings.TrimSuffix(host, ":443")
	return host
}
