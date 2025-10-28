# Module Tasks — internal/planner

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [x] [I2.E2] Maintain the JSON plan DAG schema (nodes, edges, budgets) and validation routines.
- [x] [I2.E2] Reject invalid plans with actionable errors and persist validated graphs to Postgres.
- [x] [I2.E3] Emit checkpoint metadata and dependency ordering required by the graph executor.
- [x] [I3.E3] Identify recurring sub-graphs, convert them into parameterised templates, and version them for approval.
- [x] [I3.E3] Suggest procedural templates to the orchestrator when planner confidence exceeds thresholds.
- [x] [I3.E4] Pass temporal `UpdatePolicy` inputs (refresh_interval, dedup_window, etc.) into plan generation.
