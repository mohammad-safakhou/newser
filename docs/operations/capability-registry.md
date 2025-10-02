# Capability Registry

Initiative 2 · Epic 1 introduces a signed ToolCard registry so the orchestrator only loads vetted agents.

## ToolCard Schema
Each ToolCard carries the metadata the planner/orchestrator need:
- `name`, `version`, `description`: human identifiers (semantic version strongly encouraged).
- `agent_type`: must match the agent key (e.g. `research`, `analysis`).
- `input_schema` / `output_schema`: JSON Schemas encoded as objects (the runtime persists them as JSON via `tool_registry.input_schema` / `output_schema`).
- `cost_estimate`: optional float used by the budget watchdog.
- `side_effects`: array describing observable actions (`network`, `filesystem`, etc.).
- `checksum`: SHA-256 hash of the canonical payload, generated automatically.
- `signature`: HMAC-SHA256 of the checksum using `capability.signing_secret`.

## Storage & Runtime Flow
`migrations/0007_tool_registry.*` creates the `tool_registry` table. On startup `runtime.EnsureCapabilityRegistry` seeds default ToolCards (with computed checksum/signature), loads all rows, and verifies signatures plus required agents. The planner and agent factories now consult the registry; any plan containing an unregistered tool fails validation.

## Publishing Options
1. **CLI**: `go run ./cmd/tools publish --file path/to/tool.json --config config/config.json`.
   - The command recomputes the checksum, signs the card with `capability.signing_secret`, and upserts it via Postgres.
   - `go run ./cmd/tools list` prints the stored ToolCards (after decoding JSON columns).
2. **HTTP API**: `POST /api/tools` (JWT required) accepts `{ "tool_card": { ... } }` without checksum/signature fields. The server recomputes the checksum, signs with the configured secret, and stores the record. `GET /api/tools` returns the active signed cards.

Supply only the base metadata when publishing—any stale checksum/signature in the payload will trigger a `400 checksum mismatch`.

## Validation & CI
Run `go test ./...` before publishing. Key suites (`internal/runtime`, `internal/capability`, `internal/agent/core`, `internal/server`) cover checksum/signature generation, registry seeding, planner enforcement, and the `/api/tools` endpoints. Adding new ToolCards should include schema fixtures plus corresponding tests when behaviour changes.

Rotate the signing secret by updating `capability.signing_secret`, republishing ToolCards, and restarting services so the registry reloads verified entries.
