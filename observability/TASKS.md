# Module Tasks — observability

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [x] [I1.E3] Expand OTEL collector, Prometheus, and Grafana configs to cover all new services and metrics.
- [x] [I2.E3] Visualise executor metrics (retry counts, checkpoint age, queue depth) and set alert thresholds.
- [x] [I3.E4] Build dashboards for memory health (hit/miss, summarisation latency, delta rate).
- [x] [I4.E1] Track sandboxed execution metrics (run duration, resource usage, cost per tool).
- [x] [I5.E1] Monitor crawler throughput, politeness budget, and dedup statistics.
- [ ] [I6.E2] Surface bias/fairness trends and regression alerts.
- [ ] [I7.E3] Power real-time dashboards (topics, runs, costs, health) with data sources and alert integrations.
- [ ] [FG] Ensure secret scanning, lint/unit/integration smoke, and accessibility checks run in CI pipelines.
