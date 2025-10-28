# QA Benchmark CLI

The QA harness lets you exercise a suite of synthesis and planner regression checks before pushing changes.

```bash
# Run with the default smoke benchmarks and project configuration
GOFLAGS=-mod=mod go run ./cmd/qa --config config/config.json \
  --dataset internal/agent/qa/testdata/benchmarks.json \
  --model gpt-4o-mini \
  --out runs/qa
```

Flags:

- `--config` – optional path to the application config so the CLI can reuse shared secrets.
- `--dataset` – JSON file describing the benchmarks to execute.
- `--model` – logical model identifier stored alongside the report.
- `--baseline` – previous JSON report to compare and flag regressions.
- `--out` – output directory for JSON and Markdown summaries (`runs/qa` by default).

Each run emits:

- `qa_report_<timestamp>.json` – machine-readable results suitable for CI ingestion.
- `qa_report_<timestamp>.md` – human readable summary ideal for pull requests.

Exit status is non-zero when any benchmark fails or regresses versus the provided baseline.

## Dataset Format

Datasets are JSON arrays of benchmark descriptors:

```json
[
  { "name": "synthesis_smoke", "type": "synthesis", "input": "synthesis_ok.json" },
  { "name": "planner_smoke",   "type": "planner",   "input": "plan_ok.json" }
]
```

Available benchmark types map to `internal/agent/qa` validators:

- `synthesis` – validates synthesis payloads for evidence density and timing metadata.
- `planner` – checks planner DAG structure and required task coverage.

Regressions are reported when a benchmark previously passed in the `--baseline` report but fails during the current run. This keeps the CLI suitable for local validation and CI gating workflows.
