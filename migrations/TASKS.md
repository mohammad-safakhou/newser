# Module Tasks — migrations

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [ ] [I3.E1] Create episodic memory tables (runs, steps, artifacts) to support full replay.
- [ ] [I3.E2] Enable pgvector extension and add embedding tables for runs and plan steps.
- [ ] [I3.E3] Add procedural template tables with versioning and approval metadata.
- [ ] [I3.E4] Define summarisation/pruning job tables and delta tracking structures.
- [ ] [I4.E4] Create attachment metadata tables referencing S3-compatible storage.
- [ ] [I5.E1] Store crawler state (visited URLs, last_seen_at, dedup hashes, budgets) with indexes for incremental fetch.
- [ ] [I6.E1] Add evidence metadata tables linking outputs to source snippets.
- [ ] [I6.E3] Persist signed run manifests and hash verification records.
- [ ] [I7.E1] Create Topic/Blueprint/View/Route schema tables with rollback history.
- [ ] [I7.E3] Build aggregated run/cost metrics tables for dashboards and alerting.
- [ ] [I8.E2] Support plugin registry models (manifests, signatures, installation state).
