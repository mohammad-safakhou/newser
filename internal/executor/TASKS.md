# Module Tasks — internal/executor

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`.

- [ ] [I4.E1] Implement sandboxed TaskRunner adapters for Python and Node with deterministic JSON I/O.
- [ ] [I4.E1] Enforce CPU/memory/time caps per task and persist results/artifacts to attachment storage.
- [ ] [I4.E2] Execute whitelisted terminal commands via audited wrappers and reject non-whitelisted requests.
- [ ] [I4.E3] Load black-box tool manifests, handle version rollbacks, and run reproducibility checks.
- [ ] [I4.E4] Capture structured outputs and publish attachment metadata for downstream consumers.
- [ ] [I2.E3] Feed retry counts and duration metrics back to the watcher for monitoring and alerting.
