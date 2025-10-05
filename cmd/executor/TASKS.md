# Module Tasks — cmd/executor

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [ ] [I4.E1] Launch the sandboxed Python/Node execution service with JSON I/O adapters and image hash validation.
- [ ] [I4.E1] Expose CLI flags for CPU/memory/time caps and enforce them during sandbox startup.
- [ ] [I4.E2] Register whitelisted terminal tools and audit logging sinks so the executor daemon can broker proxied commands.
- [ ] [I4.E3] Add lifecycle hooks for black-box tool manifests (registration, rollback control, reproducibility tests).
- [ ] [I4.E4] Emit artifact upload events to the attachment store once executions finish.
- [ ] [FG] Integrate OTEL metrics/traces, schema registry validation, and JWT auth for inbound task dispatch.
