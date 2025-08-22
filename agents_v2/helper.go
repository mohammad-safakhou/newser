package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
)

// ==========================================================
// Helpers
// ==========================================================

func mapToolToCapability(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasPrefix(l, "web.search"):
		return CapWebSearch
	case strings.HasPrefix(l, "web.fetch"):
		return CapWebFetch
	case strings.HasPrefix(l, "web.ingest"):
		return CapWebIngest
	case strings.HasPrefix(l, "search.query"):
		return CapSearchQuery
	case strings.HasPrefix(l, "embedding."):
		return CapEmbedding
	default:
		return ""
	}
}

func pickTool(a *Agent, capability string) string {
	cands := CapabilityTools[capability]
	for _, name := range cands {
		if _, ok := a.Tools[name]; ok {
			return name
		}
	}
	for n := range a.Tools {
		if strings.Contains(strings.ToLower(n), strings.ToLower(capability)) {
			return n
		}
	}
	for n := range a.Tools {
		return n
	}
	return ""
}

func resolveTemplates(args map[string]any, results map[string]map[string]any) map[string]any {
	// Very small templater: replaces strings like ${actions.ID.field}
	repl := func(s string) string {
		if !strings.Contains(s, "${") {
			return s
		}
		out := s
		for {
			start := strings.Index(out, "${")
			if start == -1 {
				break
			}
			end := strings.Index(out[start:], "}")
			if end == -1 {
				break
			}
			end += start
			key := out[start+2 : end]
			parts := strings.Split(key, ".") // actions.ID.field
			if len(parts) >= 3 && parts[0] == "actions" {
				id := parts[1]
				field := parts[2]
				if m, ok := results[id]; ok {
					if v, ok2 := m[field]; ok2 {
						out = out[:start] + fmt.Sprint(v) + out[end+1:]
						continue
					}
				}
			}
			// not resolved; remove braces to avoid infinite loop
			out = out[:start] + out[start+2:]
		}
		return out
	}
	out := map[string]any{}
	for k, v := range args {
		switch x := v.(type) {
		case string:
			out[k] = repl(x)
		case []any:
			var arr []any
			for _, e := range x {
				if s, ok := e.(string); ok {
					arr = append(arr, repl(s))
				} else {
					arr = append(arr, e)
				}
			}
			out[k] = arr
		case map[string]any:
			out[k] = resolveTemplates(x, results)
		default:
			out[k] = v
		}
	}
	return out
}

func randomID() string { return fmt.Sprintf("r%08x", rand.Uint32()) }
func str(v any) string { s, _ := v.(string); return s }
func toInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	default:
		return def
	}
}
func mustJSON(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
