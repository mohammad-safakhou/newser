# Module Tasks — cmd/worker

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [x] [I2.E3] Add CLI configuration for executor concurrency, checkpoint resume intervals, and backpressure thresholds.
- [x] [I2.E3] Provide a crash-resume smoke command that replays pending checkpoints to validate deterministic recovery on startup.
- [x] [I2.E4] Wire budget watchdog approvals (manual override flow) into worker startup so runs abort gracefully when caps are exceeded.
- [ ] [I3.E3] Allow enabling procedural template runners to reuse stored sub-graphs when the planner dispatches them.
- [ ] [I3.E4] Register semantic/episodic memory ingestion hooks so worker can push deltas and nightly jobs via queue messages.
- [ ] [FG] Ensure telemetry exporters, schema registry, and JWT verification share the unified runtime bootstrap.
