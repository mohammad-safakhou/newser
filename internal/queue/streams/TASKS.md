# Module Tasks — internal/queue/streams

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [ ] [I2.E3] Add consumer lag monitoring, backpressure controls, and replay helpers for deterministic DAG execution.
- [ ] [I2.E3] Persist checkpoint acknowledgements and resume tokens to survive worker crashes without duplication.
- [ ] [I4.E1] Support attachment/result events, ensuring payload schemas are validated before dispatching to executors.
- [ ] [I5.E1] Provide stream topology for crawler scheduling (sharded workers, dedup signals, refresh intervals).
- [ ] [I5.E2] Validate canonicalization/dedup schemas for crawler outputs and surface metrics on duplication.
- [ ] [FG] Enforce schema registry registration and version compatibility on every stream publish/consume path.
