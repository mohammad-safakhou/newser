package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "time"
    "github.com/mohammad-safakhou/newser/internal/agent/qa"
)

func main() {
    var synthPath string
    var planPath string
    var outDir string
    flag.StringVar(&synthPath, "synthesis", "internal/agent/qa/testdata/synthesis_ok.json", "path to synthesis json")
    flag.StringVar(&planPath, "plan", "internal/agent/qa/testdata/plan_ok.json", "path to planner json")
    flag.StringVar(&outDir, "out", "todos/qa", "output dir for results (git-ignored)")
    flag.Parse()

    type result struct{ Name string `json:"name"`; Passed bool `json:"passed"`; Error string `json:"error,omitempty"` }
    results := []result{}

    if err := qa.ValidateSynthesisFile(synthPath); err != nil {
        results = append(results, result{Name:"synthesis", Passed:false, Error:err.Error()})
    } else { results = append(results, result{Name:"synthesis", Passed:true}) }

    if err := qa.ValidatePlannerFile(planPath); err != nil {
        results = append(results, result{Name:"planner", Passed:false, Error:err.Error()})
    } else { results = append(results, result{Name:"planner", Passed:true}) }

    // Print summary
    allPass := true
    for _, r := range results { if !r.Passed { allPass = false } }
    fmt.Printf("QA results: %v\n", results)

    // Persist jsonl record under todos/qa
    _ = os.MkdirAll(outDir, 0o755)
    rec := map[string]interface{}{
        "ts": time.Now().UTC().Format(time.RFC3339),
        "type": "qa_run",
        "results": results,
    }
    b, _ := json.Marshal(rec)
    _ = os.WriteFile(filepath.Join(outDir, fmt.Sprintf("qa_%d.jsonl", time.Now().Unix())), append(b, '\n'), 0o644)
    if !allPass { os.Exit(1) }
}

