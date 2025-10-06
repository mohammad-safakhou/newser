# Module Tasks — internal/runtime

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [x] [I1.E4] Finish sandbox policy enforcement ensuring every service reports sandbox=true and aborts on violations.
- [ ] [I2.E1] Centralise capability registry bootstrap and manifest signature validation for all binaries.
- [ ] [I4.E1] Provide shared sandbox adapters (Docker/NSJail) with resource defaults used by executor services.
- [ ] [I6.E3] Offer helpers to sign run manifests and verify hashes prior to persistence/export.
- [ ] [FG] Standardise telemetry initialisation (OTEL, Prometheus) and JWT loading as reusable helpers across services.
- [ ] [FG] Expose schema registry utilities so message/plan schemas are versioned and validated consistently.
