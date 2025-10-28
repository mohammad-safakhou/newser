package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBenchmarks(t *testing.T) {
	suite, err := loadBenchmarks("../internal/agent/qa/testdata/benchmarks.json")
	if err != nil {
		t.Fatalf("loadBenchmarks: %v", err)
	}
	if len(suite) != 2 {
		t.Fatalf("expected 2 benchmarks, got %d", len(suite))
	}
	if suite[0].Type != "synthesis" {
		t.Fatalf("unexpected first type: %s", suite[0].Type)
	}
}

func TestWriteReports(t *testing.T) {
	tmpDir := t.TempDir()
	report := runReport{
		Timestamp: "2024-01-01T00:00:00Z",
		Dataset:   "dataset.json",
		Model:     "test-model",
		Results:   []benchmarkResult{{Name: "bench", Type: "synthesis", Dataset: "dataset.json", Model: "test-model", Passed: true}},
	}
	jsonPath := filepath.Join(tmpDir, "report.json")
	if err := writeJSONReport(jsonPath, report); err != nil {
		t.Fatalf("writeJSONReport: %v", err)
	}
	mdPath := filepath.Join(tmpDir, "report.md")
	if err := writeMarkdownReport(mdPath, report); err != nil {
		t.Fatalf("writeMarkdownReport: %v", err)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	var parsed runReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if parsed.Model != "test-model" {
		t.Fatalf("unexpected model: %s", parsed.Model)
	}
}
