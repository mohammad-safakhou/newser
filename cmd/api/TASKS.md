# Module Tasks — cmd/api

Roadmap alignment: reference codes use `Ix.Ey` for Initiative/Epic from `tasks.md`, and `FG` for Feasibility Gates.

- [x] [I2.E2] Replace the placeholder runtime with the full HTTP server bootstrap so CLI `cmd/api` starts the validated plan/dry-run API surface.
- [x] [I2.E2] Surface CLI flags/env for plan validation cost estimation modes and ensure dry-run routes are enabled/disable-able per environment.
- [ ] [I3.E2] Add service startup options to toggle semantic memory search endpoints once `internal/server` wiring lands.
- [ ] [I6.E3] Expose signed run manifest export endpoints (hash verification, immutable logs) through command-line configuration.
- [ ] [I7.E3] Provide metrics/WebSocket feature toggles for dashboard live updates so the CLI can run in headless environments.
- [ ] [FG] Ensure CLI bootstraps shared auth (JWT secret), schema registry, and OTEL metrics exporters to satisfy feasibility baselines.
