package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	agentqa "github.com/mohammad-safakhou/newser/internal/agent/qa"
)

type benchmark struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Input  string `json:"input"`
	Target string `json:"target,omitempty"`
}

type benchmarkResult struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Dataset    string `json:"dataset"`
	Model      string `json:"model"`
	Passed     bool   `json:"passed"`
	Regression bool   `json:"regression,omitempty"`
	Error      string `json:"error,omitempty"`
}

type runReport struct {
	Timestamp string            `json:"timestamp"`
	Config    string            `json:"config"`
	Dataset   string            `json:"dataset"`
	Model     string            `json:"model"`
	Results   []benchmarkResult `json:"results"`
}

type baselineReport struct {
	Results []benchmarkResult `json:"results"`
}

func main() {
	var cfgPath string
	var datasetPath string
	var model string
	var baselinePath string
	var outDir string

	flag.StringVar(&cfgPath, "config", "", "path to config file (optional)")
	flag.StringVar(&datasetPath, "dataset", "internal/agent/qa/testdata/benchmarks.json", "benchmark suite to run")
	flag.StringVar(&model, "model", "default", "model identifier used for the run")
	flag.StringVar(&baselinePath, "baseline", "", "previous run report to compare regressions")
	flag.StringVar(&outDir, "out", filepath.Join("runs", "qa"), "directory for QA reports")
	flag.Parse()

	cfg := config.LoadConfig(cfgPath)
	_ = cfg // keep for parity with other services

	suite, err := loadBenchmarks(datasetPath)
	if err != nil {
		exitErr(err)
	}
	if len(suite) == 0 {
		exitErr(fmt.Errorf("benchmark dataset %s is empty", datasetPath))
	}

	baseline, err := loadBaseline(baselinePath)
	if err != nil {
		exitErr(err)
	}

	results := executeBenchmarks(context.Background(), suite, datasetPath, model)
	annotateRegressions(results, baseline)

	report := runReport{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Config:    cfgPath,
		Dataset:   datasetPath,
		Model:     model,
		Results:   results,
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		exitErr(fmt.Errorf("create output dir: %w", err))
	}
	jsonPath := filepath.Join(outDir, fmt.Sprintf("qa_report_%d.json", time.Now().Unix()))
	if err := writeJSONReport(jsonPath, report); err != nil {
		exitErr(err)
	}
	mdPath := strings.TrimSuffix(jsonPath, ".json") + ".md"
	if err := writeMarkdownReport(mdPath, report); err != nil {
		exitErr(err)
	}

	printSummary(report)
	if hasFailures(report.Results) {
		os.Exit(1)
	}
}

func loadBenchmarks(path string) ([]benchmark, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %s: %w", path, err)
	}
	var suite []benchmark
	if err := json.Unmarshal(b, &suite); err != nil {
		return nil, fmt.Errorf("decode dataset %s: %w", path, err)
	}
	return suite, nil
}

func executeBenchmarks(ctx context.Context, suite []benchmark, datasetPath, model string) []benchmarkResult {
	baseDir := filepath.Dir(datasetPath)
	results := make([]benchmarkResult, 0, len(suite))
	for _, bench := range suite {
		input := bench.Input
		if !filepath.IsAbs(input) {
			input = filepath.Join(baseDir, input)
		}
		res := benchmarkResult{
			Name:    bench.Name,
			Type:    bench.Type,
			Dataset: datasetPath,
			Model:   model,
			Passed:  true,
		}
		var err error
		switch strings.ToLower(bench.Type) {
		case "synthesis":
			err = agentqa.ValidateSynthesisFile(input)
		case "planner":
			err = agentqa.ValidatePlannerFile(input)
		default:
			err = fmt.Errorf("unknown benchmark type %q", bench.Type)
		}
		if err != nil {
			res.Passed = false
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
}

func loadBaseline(path string) (map[string]bool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var report baselineReport
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, fmt.Errorf("decode baseline %s: %w", path, err)
	}
	baseline := make(map[string]bool, len(report.Results))
	for _, r := range report.Results {
		baseline[r.Name] = r.Passed
	}
	return baseline, nil
}

func annotateRegressions(results []benchmarkResult, baseline map[string]bool) {
	if len(baseline) == 0 {
		return
	}
	for i := range results {
		prev, ok := baseline[results[i].Name]
		if ok && prev && !results[i].Passed {
			results[i].Regression = true
		}
	}
}

func writeJSONReport(path string, report runReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func writeMarkdownReport(path string, report runReport) error {
	var b strings.Builder
	b.WriteString("# QA Benchmark Report\n\n")
	b.WriteString(fmt.Sprintf("- Timestamp: %s\n", report.Timestamp))
	if report.Config != "" {
		b.WriteString(fmt.Sprintf("- Config: `%s`\n", report.Config))
	}
	b.WriteString(fmt.Sprintf("- Dataset: `%s`\n", report.Dataset))
	b.WriteString(fmt.Sprintf("- Model: `%s`\n\n", report.Model))
	b.WriteString("| Benchmark | Type | Result | Regression |\n")
	b.WriteString("|-----------|------|--------|------------|\n")
	for _, r := range report.Results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		reg := ""
		if r.Regression {
			reg = "⚠"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", r.Name, r.Type, status, reg))
		if r.Error != "" {
			b.WriteString(fmt.Sprintf("| ↳ | | %s | |\n", r.Error))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func printSummary(report runReport) {
	sort.Slice(report.Results, func(i, j int) bool {
		return report.Results[i].Name < report.Results[j].Name
	})
	passed := 0
	regressions := 0
	for _, r := range report.Results {
		if r.Passed {
			passed++
		}
		if r.Regression {
			regressions++
		}
	}
	fmt.Printf("QA Summary (%s on %s)\n", report.Model, report.Dataset)
	fmt.Printf("  Passed %d/%d | Regressions: %d\n", passed, len(report.Results), regressions)
	for _, r := range report.Results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		if r.Regression {
			status += " (regression)"
		}
		line := fmt.Sprintf("  - %s: %s", r.Name, status)
		if r.Error != "" {
			line += fmt.Sprintf(" — %s", r.Error)
		}
		fmt.Println(line)
	}
}

func hasFailures(results []benchmarkResult) bool {
	for _, r := range results {
		if !r.Passed || r.Regression {
			return true
		}
	}
	return false
}

func exitErr(err error) {
	if err == nil {
		return
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		fmt.Fprintf(os.Stderr, "qa: %s\n", pathErr.Error())
	} else {
		fmt.Fprintf(os.Stderr, "qa: %v\n", err)
	}
	os.Exit(1)
}
