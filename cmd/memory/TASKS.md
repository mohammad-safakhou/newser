# Module Tasks — cmd/memory

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [x] [I3.E4] Stand up the memory manager service exposing `memory.search/write/summarize` APIs with unified auth.
- [x] [I3.E4] Schedule nightly summarisation and pruning jobs, parameterised by retention/dedup policy.
- [x] [I3.E4] Emit memory health metrics (size, hit/miss, clustering status) to the observability stack.
- [x] [I3.E4] Provide CLI hooks to trigger `memory.delta` calculations and rebuild semantic indexes.
- [x] [I3.E2] Optionally rebuild pgvector embeddings on startup when configured.
- [x] [FG] Share schema registry bootstrap, OTEL exporters, and JWT validation logic with other services.
