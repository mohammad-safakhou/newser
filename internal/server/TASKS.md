# Module Tasks — internal/server

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [x] [I2.E2] Implement the plan JSON dry-run endpoint with schema validation, cost estimation, and clear error responses.
- [ ] [I2.E4] Add budget configuration APIs, breach reporting, and manual approval flows with audit trails.
- [x] [I3.E2] Expose `/memory.search` and related semantic memory routes with pgvector-backed queries.
- [ ] [I3.E4] Wire `memory.write/summarize/delta` endpoints once the memory manager is available, enforcing auth scopes.
- [ ] [I6.E1] Include source IDs/snippets in aggregation responses and reject unreferenced claims server-side.
- [ ] [I6.E3] Generate and persist signed run manifests, exposing export/download APIs with hash verification.
- [ ] [I6.E4] Provide planner decision and memory hit inspection APIs to power the explainability UI.
- [ ] [I7.E1] Support conversational builder schemas (Topic/Blueprint/View/Route) with diff + rollback endpoints.
- [ ] [I7.E3] Stream run status/cost metrics via WebSocket or SSE for the dashboard and alert hooks.
- [ ] [FG] Ensure unified JWT auth, schema registry validation, and OTEL instrumentation cover all new routes.
