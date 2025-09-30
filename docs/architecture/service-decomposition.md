# Service Decomposition Plan (Initiative 1 / Epic 1 / Task 1)

This document captures the audit of the current monolith and defines the first-pass boundaries for splitting Newser into independently deployable services that communicate through Redis Streams. It is scoped to Initiative 1, Epic 1 and informs subsequent tasks (queue libraries, scaffolding, checkpointing, schema registry, and crash tests).

## 1. Current Monolith Snapshot

- `cmd/serve` bootstraps a single process that owns HTTP APIs, topic scheduling, run orchestration, and persistence side effects.
- `internal/server` mixes concerns: Echo HTTP layers, Redis-backed cron scheduler, run processing pipeline (`processRun`), telemetry endpoints, and authentication.
- `internal/agent/core` provides the orchestration brain (planner, agents, sources, synthesis) but runs synchronously in-process.
- `internal/store` encapsulates Postgres access for topics, runs, processing results, highlights, and chat history.
- Side effects such as crawling, tool execution, and memory persistence occur inside the same process space, making failure isolation and horizontal scaling difficult.

## 2. Target Services & Responsibilities

| Service | Responsibilities | Storage | Inbound Messages | Outbound Messages |
|---------|------------------|---------|------------------|-------------------|
| **API** (`cmd/api`) | HTTP auth, topic CRUD, manual run triggers, surfacing run state/results. Publishes work orders and reads projections. | Postgres (read/write), Redis (session), object storage (reads). | `run.triggered` (HTTP -> publish) | `run.enqueued` (ack), `topic.updated` (future) |
| **Worker** (`cmd/worker`) | Consumes run requests, allocates run IDs, coordinates orchestration lifecycle, emits task dispatches, aggregates task results, marks completion. | Postgres (runs, processing_results), Redis Streams (checkpoint state). | `run.enqueued`, `task.result`, `memory.signal` | `task.dispatch`, `run.completed`, `crawl.request` |
| **Executor** (`cmd/executor`) | Executes agent tasks (research, analysis, synthesis, conflict, highlight, KG) inside sandboxed toolchains; handles LLM calls. | Object storage for artifacts, Redis for sandbox leases. | `task.dispatch` | `task.result`, `executor.telemetry` |
| **Crawler** (`cmd/crawler`) | Performs outbound HTTP fetches, RSS/sitemap discovery, enrichment; respects policy engine. | Postgres/MinIO for cached pages, Redis for politeness locks. | `crawl.request` | `crawl.result`, `crawler.telemetry` |
| **Memory** (`cmd/memory`) | Manages episodic/semantic/procedural data, knowledge graphs, highlights. Handles read/write APIs for planner and worker. | Postgres (`processing_results`, `knowledge_graphs`), pgvector, object store. | `memory.write`, `run.completed` | `memory.signal`, `memory.snapshot` |

Additional cross-cutting components:
- **Schema Registry** (Task 5): hosts JSON Schema definitions for message payloads and enforces version compatibility during publish/consume.
- **Policy/Sandbox service** (from Epic 4) will be leveraged by Executor and Crawler, but the integration points are documented here for future use.

## 3. Message Contracts (initial draft)

All message envelopes include `{ event_id, event_type, occurred_at, trace_id, attempt, payload_version }` to support idempotency and observability.

1. `run.enqueued`
   - Producers: API, Scheduler.
   - Consumers: Worker.
   - Payload: `{ run_id?, topic_id, user_id, trigger: "manual|schedule", preferences_snapshot, context_snapshot }`.
   - Notes: Worker is responsible for generating `run_id` when absent and persisting the run stub before acking.

2. `task.dispatch`
   - Producers: Worker.
   - Consumers: Executor.
   - Payload: `{ run_id, task_id, task_type, priority, plan_snapshot, parameters, checkpoint_token }`.
   - Notes: `checkpoint_token` ties back to Worker state; Executor must echo it in the result.

3. `task.result`
   - Producers: Executor.
   - Consumers: Worker.
   - Payload: `{ run_id, task_id, success, output, cost_estimate, tokens_used, artifacts[], checkpoint_token }`.
   - Notes: Worker updates checkpoints idempotently using `(run_id, task_id)` plus `checkpoint_token` as the idempotency key.

4. `crawl.request`
   - Producers: Worker, Executor (when a task requires URLs not cached).
   - Consumers: Crawler.
   - Payload: `{ run_id, request_id, urls[], freshness_hint, policy_profile }`.
   - Notes: Crawler responds via `crawl.result` stream or pushes to shared cache; Worker listens for `crawler.telemetry` for observability.

5. `run.completed`
   - Producers: Worker.
   - Consumers: API (for websockets/notifications), Memory (to ingest episodic data), Dashboard.
   - Payload: `{ run_id, topic_id, status, summary, detailed_report_ref, metrics }`.

6. `memory.signal`
   - Producers: Memory service (after compaction/search updates).
   - Consumers: Worker (planner context refresh), API (cache invalidation).
   - Payload: `{ topic_id, signal_type, reference_id }`.

These schemas will be formalized and versioned in Task 5 via the schema registry. For now they anchor library design (Task 2) and compose scaffolding (Task 3).

## 4. Boundary Impacts on Existing Packages

- `internal/server` will split into `internal/api` (HTTP) and standalone scheduler binary. Scheduler becomes a lightweight producer that emits `run.enqueued` instead of calling `processRun` directly.
- `internal/agent/core` remains the orchestration engine but moves behind the Worker service. APIs must become stateless to support checkpointing; Planner/Executor coordination will leverage the message contracts.
- `internal/store` evolves into shared libraries consumed by API, Worker, and Memory. Write-heavy operations (e.g., `UpsertProcessingResult`) migrate to Worker/Memory, while API provides read projections only.
- Redis locks used for scheduling migrate to dedicated streams with consumer groups; idempotency keys align with `event_id`.

## 5. Acceptance Traceability & Next Steps

- Acceptance criterion "Services start independently" translates to delivering distinct binaries defined above and Compose definitions in Task 3.
- "Message schemas validated" maps to Task 5; this doc provides the baseline field list for schema evolution.
- "Worker resumes without duplication" relies on `checkpoint_token` usage plus idempotent DB writes outlined here; detailed implementation will arrive in Task 4 and Task 6.
- Observability alignment ("metrics visible in Grafana") requires each service to emit traces/metrics tagged with `service.name`; instrumentation strategy will be documented alongside Task 3 and Epic 3.

This design will be refined as downstream tasks add concrete schemas and runtime behaviors, but it provides the shared vocabulary required to proceed with queue libraries and service scaffolding.
