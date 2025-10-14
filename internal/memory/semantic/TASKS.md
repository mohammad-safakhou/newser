# Module Tasks — internal/memory/semantic

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [x] [I3.E2] Implement pgvector embedding generation, ingestion pipelines, and similarity search queries.
- [x] [I3.E2] Optimise search latency (<200 ms) and evaluate recall/precision to meet acceptance criteria.
- [ ] [I3.E4] Support nightly summarisation and pruning hooks that reduce memory footprint.
- [x] [I3.E4] Provide `memory.delta` helpers comparing embeddings against dedup windows and returning novel content.
- [x] [I3.E4] Emit telemetry (hit/miss, drift, vector rebuild status) for observability dashboards.
