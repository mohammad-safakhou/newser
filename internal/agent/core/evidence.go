package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func buildEvidence(items []map[string]interface{}, lookup map[string]*Source) []Evidence {
	if len(items) == 0 {
		return nil
	}
	evidence := make([]Evidence, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		id := ""
		if v, ok := item["id"].(string); ok && strings.TrimSpace(v) != "" {
			id = strings.TrimSpace(v)
		} else {
			id = uuid.NewString()
		}
		statement := extractStatement(item)
		if statement == "" {
			continue
		}
		sourceIDs := extractStringSlice(item["source_ids"])
		if len(sourceIDs) == 0 {
			continue
		}

		ev := Evidence{
			ID:        id,
			Statement: statement,
			SourceIDs: sourceIDs,
			Metadata:  make(map[string]interface{}),
			Sources:   extractEvidenceSources(item["sources"], lookup),
		}
		if cat, ok := item["category"].(string); ok && strings.TrimSpace(cat) != "" {
			ev.Category = strings.TrimSpace(cat)
		}
		if score, ok := asFloat(item["score"]); ok {
			ev.Score = score
		}
		// Preserve ancillary metadata for future UI
		for key, value := range item {
			switch key {
			case "id", "summary", "title", "source_ids", "category", "score":
				continue
			}
			ev.Metadata[key] = value
		}
		evidence = append(evidence, ev)
	}
	if len(evidence) == 0 {
		return nil
	}
	return evidence
}

func extractStatement(item map[string]interface{}) string {
	if v, ok := item["summary"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := item["title"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := item["statement"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := item["content"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

func extractStringSlice(raw interface{}) []string {
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func asFloat(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case jsonNumber:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// jsonNumber is satisfied by encoding/json.Number without introducing import cycle.
type jsonNumber interface {
	Float64() (float64, error)
}

func evidenceMetadataTimestamp(item map[string]interface{}, key string) *time.Time {
	if v, ok := item[key].(string); ok && v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			return &ts
		}
	}
	return nil
}

func extractEvidenceSources(raw interface{}, lookup map[string]*Source) []EvidenceSource {
	var entries []map[string]interface{}
	switch v := raw.(type) {
	case []map[string]interface{}:
		entries = v
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				entries = append(entries, m)
			}
		}
	default:
		return nil
	}
	if len(entries) == 0 {
		return nil
	}
	out := make([]EvidenceSource, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		id := strings.TrimSpace(stringFromAny(entry["id"]))
		url := strings.TrimSpace(stringFromAny(entry["url"]))
		title := strings.TrimSpace(stringFromAny(entry["title"]))
		domain := strings.TrimSpace(stringFromAny(entry["domain"]))
		if domain == "" && url != "" {
			domain = toDomain(url)
		}
		snippet := strings.TrimSpace(stringFromAny(entry["snippet"]))
		if snippet == "" && id != "" && lookup != nil {
			if src, ok := lookup[id]; ok && src != nil {
				snippet = trimSnippet(src.Summary)
				if snippet == "" {
					snippet = trimSnippet(src.Content)
				}
			}
		}
		cred := 0.0
		if id != "" && lookup != nil {
			if src, ok := lookup[id]; ok && src != nil {
				cred = src.Credibility
			}
		}
		if cred == 0 {
			if v, ok := asFloat(entry["credibility"]); ok {
				cred = v
			}
		}
		out = append(out, EvidenceSource{
			ID:          id,
			Title:       title,
			URL:         url,
			Domain:      domain,
			Snippet:     snippet,
			Credibility: cred,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringFromAny(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case jsonNumber:
		if s, ok := v.(fmt.Stringer); ok {
			return s.String()
		}
		if f, err := v.Float64(); err == nil {
			return fmt.Sprintf("%.3f", f)
		}
		return ""
	case float64:
		return fmt.Sprintf("%.3f", v)
	case float32:
		return fmt.Sprintf("%.3f", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return ""
	}
}
