# Module Tasks — internal/worker

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [x] [I2.E3] Expand DAG executor integration to handle checkpoints, retries, and circuit breaking for every task stage.
- [x] [I2.E3] Emit executor metrics (retry counts, checkpoint age, task durations) for observability dashboards.
- [x] [I2.E4] Enforce budget watchdog checks before dispatching tasks; publish breach events to approval queues.
- [ ] [I3.E3] Detect reusable sub-graphs and request procedural templates from memory when available.
- [ ] [I3.E4] Notify memory manager about run deltas and summarisation needs after task completion.
- [ ] [I4.E4] Attach artifact metadata to task completion events so the attachment store stays in sync.
