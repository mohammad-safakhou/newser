# Module Tasks — internal/store

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [ ] [I2.E2] Persist validated plan graphs, budgets, and execution orders with version history.
- [ ] [I2.E4] Store budget approvals, breach reports, and override audit trails.
- [ ] [I3.E1] Capture episodic memory artifacts (plans, prompts, tool outputs) for full run replay.
- [ ] [I3.E2] Manage run and plan-step embeddings (pgvector) with efficient search indices.
- [ ] [I3.E3] Save procedural templates and track reuse metrics.
- [ ] [I3.E4] Record summarisation/pruning metadata and memory deltas by topic.
- [ ] [I4.E4] Store artifact metadata (S3 URIs, retention info) for the attachment store.
- [ ] [I5.E1] Persist crawl state (visited URLs, retry depth, last_seen_at) and dedup hashes.
- [ ] [I6.E1] Link evidence metadata (source IDs, snippets) to every claim/output.
- [x] [I6.E3] Maintain immutable signed run manifests with hash verification fields.
- [ ] [I7.E3] Aggregate run status/cost metrics for dashboards and alert hooks.
