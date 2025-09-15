package qa

import (
    "encoding/json"
    "fmt"
    "os"
)

// ValidateSynthesisFile loads a synthesis JSON (LLM output shape) and validates core constraints.
func ValidateSynthesisFile(path string) error {
    b, err := os.ReadFile(path)
    if err != nil { return err }
    var m map[string]interface{}
    if err := json.Unmarshal(b, &m); err != nil { return fmt.Errorf("invalid json: %w", err) }
    items, _ := m["items"].([]interface{})
    if len(items) < 6 || len(items) > 12 { return fmt.Errorf("items count out of range: %d", len(items)) }
    for i, it := range items {
        mm, ok := it.(map[string]interface{})
        if !ok { return fmt.Errorf("item %d not an object", i) }
        if s, _ := mm["published_at"].(string); s == "" { return fmt.Errorf("item %d missing published_at", i) }
        srcs, _ := mm["sources"].([]interface{})
        if len(srcs) == 0 { return fmt.Errorf("item %d has no sources", i) }
    }
    return nil
}

// ValidatePlannerFile loads a planner JSON (plan result) and validates task presence/order.
func ValidatePlannerFile(path string) error {
    b, err := os.ReadFile(path)
    if err != nil { return err }
    var m struct{
        Tasks []struct{ ID, Type string `json:"id"` } `json:"tasks"`
    }
    if err := json.Unmarshal(b, &m); err != nil { return fmt.Errorf("invalid json: %w", err) }
    if len(m.Tasks) == 0 { return fmt.Errorf("no tasks present") }
    hasKG := false
    for _, t := range m.Tasks { if t.Type == "knowledge_graph" { hasKG = true; break } }
    if !hasKG { return fmt.Errorf("knowledge_graph task missing") }
    if m.Tasks[len(m.Tasks)-1].Type != "synthesis" { return fmt.Errorf("final task is not synthesis") }
    return nil
}

