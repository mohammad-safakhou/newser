# Module Tasks — internal/memory

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [x] [I3.E2] Generate embeddings for runs/sub-plans using pgvector and expose `/memory.search` queries.
- [x] [I3.E2] Integrate semantic search into planner pre-flight and evaluate recall/precision targets.
- [x] [I3.E3] Store procedural templates derived from recurring sub-graphs, including approval/version workflows.
- [x] [I3.E4] Implement `memory.search/write/summarize` logic with TTL, clustering, and pruning strategies.
- [x] [I3.E4] Build the `memory.delta` function to filter previously seen content within dedup windows.
- [x] [I3.E4] Expose memory health metrics (size, hit/miss, summarisation latency) for observability.
