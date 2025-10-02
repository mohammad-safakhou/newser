# 🧭 Newser — Final Open-Source Implementation Roadmap

Newser is an open-source, AI-driven, dynamic news aggregation and synthesis system.  
This roadmap breaks the future architecture into **8 initiatives**, each containing **epics**, **tasks**, and **Acceptance Criteria**
.

---

## **Initiative 1 — Core Platform & Safety Foundation**
**Depends on:** None  
**Goal:** Lay the foundation for a secure, observable, modular platform.

---

### **Epic 1 — Service Decomposition (API, Worker, Executor, Crawler, Memory)**
**Description:**  
Split the Go monolith into distinct services communicating via durable message queues. Establish isolation, reliability, and schema governance.

**Tasks**
- [x] Audit current code (`internal/server`, scheduler, `internal/agent/core`) and define service boundaries with clear message responsibilities.
- [x] Choose Redis Streams; implement shared publish/consume libraries with schema validation and acknowledgment.
- [x] Scaffold new binaries (`cmd/api`, `cmd/worker`, `cmd/executor`, `cmd/crawler`, `cmd/memory`) and extend Docker Compose for multi-service deployment.
- [x] Implement checkpointed queue processing with idempotency keys and Postgres/Redis persistence.
- [x] Add a **Schema Registry** (JSON Schema/Protobuf) to validate and version message formats.
- [x] Add integration test simulating a worker crash and verifying resume from checkpoint.

**Acceptance Criteria**

- [x] All services start independently in Docker Compose.
- [x] Message schemas are validated through registry.
- [x] Crashed worker resumes job without duplication.

---

### **Epic 2 — Secrets & Config Hygiene**
**Description:**  
Centralize secrets and environment configuration. Remove hard-coded credentials, enforce rotation, and secure CI/CD.

**Tasks**
- [x] Inventory existing configuration and remove any leaked secrets.
- [x] Introduce `.env` templates and integrate SOPS/Vault for runtime secret injection.
- [x] Update CI/CD to support masked secrets and local dev fallbacks.
- [x] Add automated secret scanning (Gitleaks) with CI enforcement.

**Acceptance Criteria**

- [x] No secrets exist in repo or sample configs.
- [x] Secrets rotate successfully and CI blocks on leakage.
- [x] Developers can start system via `.env` without manual hacks.

---

### **Epic 3 — Observability (OpenTelemetry + Prometheus/Grafana)**
**Description:**  
Provide full observability across all services with unified tracing and metrics.

**Tasks**
- [x] Instrument services with OpenTelemetry for spans (tokens, durations, cost).
- [x] Deploy OTLP Collector + Prometheus scraping in Compose.
- [x] Build Grafana dashboards: latency, queue depth, run duration, token costs.
- [x] Add alerting thresholds (queue backlog, API latency).
- [x] Aggregate cost telemetry per topic nightly.

**Acceptance Criteria**

- [x] Traces visible in Grafana for all services.
- [x] Alerts trigger when thresholds exceeded.
- [x] Token/cost telemetry captured correctly.
- [x] System metrics visible in Grafana.

---

### **Epic 4 — Security & Policy Engine**
**Description:**  
Ensure every execution runs inside an auditable sandbox with strict resource and network limits.

**Tasks**
- [x] Evaluate NSJail vs Docker for sandbox execution.
- [x] Define security policy config (allowlists, resource/time/memory caps).
- [x] Integrate policy enforcement before each executor launch.
- [x] Add HTML sanitization and XSS regression tests.
- [x] Extract unified JWT auth shared across all binaries.

**Acceptance Criteria**

- [x] All tool runs show sandbox = true in logs.
- [x] Policy violations safely abort execution.
- [x] No XSS vulnerabilities.
- [x] Auth consistent across services.

---

## **Initiative 2 — Dynamic Orchestration Engine**
**Depends on:** Initiative 1  
**Goal:** Enable flexible, LLM-driven execution plans validated and budget-controlled.

---

### **Epic 1 — Capability Registry (ToolCards)**
**Description:**  
Formal catalog of all available tools and their safe usage metadata.

**Tasks**
- [x] Design `ToolCard` schema (I/O, cost, side effects).
- [x] Build signed registry service/API backed by Postgres.
- [x] Add CLI to publish, sign, and validate manifests.
- [x] Make orchestrator load only registered tools.

**Acceptance Criteria**

- [x] Unknown tools rejected.
- [x] All tools versioned and signed.
- [x] Registry validated in CI.

---

### **Epic 2 — Planner Model → JSON Plan Graph**
**Description:**  
Planner LLM outputs JSON DAG representing dynamic multi-step workflows.

**Tasks**
- [x] Define JSON schema for plans (nodes, edges, budgets).
- [x] Add plan validation and rejection logic in orchestrator.
- [x] Fine-tune LLM to emit schema-compliant plans.
- [x] Persist validated plan graphs in Postgres.
- [x] Add a “dry-run” endpoint for plan validation/costing.

**Acceptance Criteria**

- [x] Invalid plans rejected with clear errors.
- [x] Plans persisted and viewable via API.
- [x] Dry-run returns cost/time estimates.

---

### **Epic 3 — Graph Executor (Checkpoints, Retries, Backpressure)**
**Description:**  
Execute plans deterministically with concurrency, retries, and checkpoint recovery.

**Tasks**
- [x] Build DAG executor enforcing dependency order.
- [x] Add checkpoint persistence for resume after crash.
- [x] Implement retry and circuit-breaking strategies.
- [x] Expose executor metrics (retry counts, checkpoint age).
- [x] Implement Temporal Policy Engine:
  - [x] Define `UpdatePolicy` schema (refresh_interval, dedup_window, repeat_mode, freshness_threshold).
  - [x] Pass this policy to the planner for query strategy generation.
  - [x] Persist per-topic in Postgres and expose in WebUI builder.

**Acceptance Criteria**

- [x] Partial runs resume correctly.
- [x] Flaky tool retries capped.
- [x] Executor metrics visible.
- [x] Planner can adapt its plan structure based on user’s temporal policy (confirmed by inspecting plan JSON).

---

### **Epic 4 — Budget Watchdog**
**Description:**  
Monitor and restrict cost/time/token budgets.

**Tasks**
  - [x] Add budget configs per topic/run.
  - [x] Estimate budgets pre-run using ToolCard metadata.
  - [x] Abort gracefully when over budget and emit detailed report.
  - [x] Implement approval UI for manual overrides.

**Acceptance Criteria**

- [x] Runs stop cleanly when budget breached.
- [x] Approval flow audited.
- [x] Cost telemetry accurate.

---

## **Initiative 3 — Memory & Experience System**
**Depends on:** Initiatives 1 & 2  
**Goal:** Persist experiences and knowledge for contextual reasoning and reuse.

---

### **Epic 1 — Episodic Memory**
**Description:**  
Store complete run history with traceable outputs.

**Tasks**
- [ ] Design DB schema to capture plan, prompts, tool outputs, and artifacts.
- [ ] Persist episodic data transactionally.
- [ ] Build replay API/CLI to inspect or re-execute.
- [ ] Add retention/cleanup jobs.

**Acceptance Criteria**

- [ ] Runs replay identically.
- [ ] Retention job deletes old records.
- [ ] Replay verified in tests.

---

### **Epic 2 — Semantic Memory (pgvector)**
**Description:**  
Provide vector-based semantic search for past runs.

**Tasks**
- [ ] Enable pgvector extension and run migrations.
- [ ] Generate embeddings per run/sub-plan.
- [ ] Expose `/memory.search` API returning similar runs.
- [ ] Integrate `memory.search` pre-flight in planner.
- [ ] Evaluate recall/precision.

**Acceptance Criteria**

- [ ] Relevant runs retrieved under 200 ms.
- [ ] ≥80 % recall maintained.
- [ ] Planner shows improved contextual reasoning.

---

### **Epic 3 — Procedural Memory**
**Description:**  
Extract and reuse sub-plans as reusable templates (skills).

**Tasks**
- [ ] Identify frequently recurring sub-graphs.
- [ ] Define parameterized template format.
- [ ] Add versioning and approval workflow.
- [ ] Integrate template suggestions into planner.

**Acceptance Criteria**

- [ ] Template reuse visible in ≥30 % of plans.
- [ ] Versioning enforced.
- [ ] Planner accuracy improved.

---

### **Epic 4 — Memory Tools & Manager**
**Description:**  
Service responsible for memory management, summarization, and pruning.

**Tasks**
- [ ] Implement `memory.search/write/summarize` APIs with auth.
- [ ] Add nightly summarization job compressing memory.
- [ ] Implement pruning, TTL, clustering.
- [ ] Expose dashboards for health metrics.
- [ ] Implement memory.delta() function:
    - Given new items, compare embeddings against prior entries within dedup_window.
    - Return only novel or changed content.
    - Integrate delta detection into synthesis flow.

**Acceptance Criteria**

- [ ] Memory size bounded.
- [ ] Summarization jobs complete nightly.
- [ ] Dashboard shows hit/miss rates.
- [ ] System correctly excludes previously seen content when user requests “new only,” verified via test plan.

---

## **Initiative 4 — Tool Execution Environment**
**Depends on:** Initiatives 1 & 2  
**Goal:** Allow secure execution of LLM-generated code and system commands.

---

### **Epic 1 — Code Runners (Python, Node)**
**Description:**  
Provide isolated, reproducible environments for executing snippets.

**Tasks**
- [ ] Package sandboxed Python/Node images with JSON I/O adapter.
- [ ] Maintain version-locked base image hashes in ToolCard registry.
- [ ] Enforce CPU/memory/time caps.
- [ ] Add integration tests capturing outputs/artifacts.

**Acceptance Criteria**

- [ ] Code runs reproducibly.
- [ ] Limits enforced.
- [ ] Structured outputs validated.

---

### **Epic 2 — Terminal Tools (Whitelist)**
**Description:**  
Expose a small, safe set of terminal commands with full auditing.

**Tasks**
- [ ] Maintain signed whitelist (`curl`, `jq`, `grep`, `headless_browser`).
- [ ] Wrap all commands with audit logging.
- [ ] Route network I/O through proxy enforcing allowlists.
- [ ] Block non-whitelisted tools with explicit error messages.

**Acceptance Criteria**

- [ ] Unlisted commands blocked.
- [ ] Proxy enforces policies.
- [ ] Logs include latency and bytes transferred.

---

### **Epic 3 — Black-Box Apps as Tools**
**Description:**  
Integrate external executables or containers as callable modules.

**Tasks**
- [ ] Define manifest format (I/O schema, version, resources).
- [ ] Build container templates and registration flow.
- [ ] Support rollback between versions.
- [ ] Add reproducibility tests.

**Acceptance Criteria**

- [ ] Manifests validated.
- [ ] Version rollbacks work.
- [ ] Identical input → identical output.

---

### **Epic 4 — Result Capture & Attachment Store**
**Description:**  
Centralize artifact storage and retrieval.

**Tasks**
- [ ] Provision S3-compatible storage (e.g., MinIO).
- [ ] Upload artifacts with metadata after execution.
- [ ] Store references in DB and expose via API.
- [ ] Show downloadable artifacts in UI.

**Acceptance Criteria**

- [ ] Artifacts downloadable and linked.
- [ ] Retention rules applied.
- [ ] Integration tests pass.

---

## **Initiative 5 — Web-Scale Crawling & Indexing**
**Depends on:** Initiatives 1 & 4  
**Goal:** Continuously crawl, extract, and index news data globally in compliance with ethical standards.

---

### **Epic 1 — Distributed Crawler**
**Description:**  
Parallel crawler with politeness, budgets, and scalability.

**Tasks**
- [ ] Design crawl scheduler and sharded workers.
- [ ] Implement robots.txt parsing and sitemap/RSS discovery.
- [ ] Persist crawl state (visited URLs, retry depth).
- [ ] Add per-domain crawl budgets and rate control.
- [ ] Provide scaling runbook.
- [ ] Enhance crawler to perform incremental fetching:
    - Store last_seen_at per URL/topic.
    - Implement semantic diff vs previous content hash.
    - Respect user-defined `dedup_window` and `refresh_interval` settings.

**Acceptance Criteria**

- [ ] Polite crawling confirmed via logs.
- [ ] Adding worker increases throughput.
- [ ] Budgets respected per domain.
- [ ] Crawler logs show skip/fetch decisions based on user-defined rules; dedup windows configurable per topic.

---

### **Epic 2 — Deduplication & Canonicalisation**
**Description:**  
Eliminate duplicate URLs and content.

**Tasks**
- [ ] Implement URL canonicalization (protocol, host, UTM strip).
- [ ] Compute SimHash/MinHash fingerprints.
- [ ] Add dedup service filtering near-duplicates.
- [ ] Track and visualize duplication rate.

**Acceptance Criteria**

- [ ] Duplicate rate < 5 %.
- [ ] Canonical URLs unique.
- [ ] Dedup metrics available.

---

### **Epic 3 — Content Extraction & Index API**
**Description:**  
Extract structured article data and make it searchable.

**Tasks**
- [ ] Build extraction pipeline (title, author, date, body) using headless browser.
- [ ] Extend Postgres schema for documents and embeddings.
- [ ] Add nightly vector re-indexing job.
- [ ] Implement hybrid lexical + semantic search API.
- [ ] Integrate with planner search tools.

**Acceptance Criteria**

- [ ] >95 % metadata accuracy on sample.
- [ ] Retrieval latency < 300 ms.
- [ ] Re-index job stable.

---

### **Epic 4 — Ethical Boundaries**
**Description:**  
Comply with copyright and robots.txt norms.

**Tasks**
- [ ] Define disallow/paywall/attribution rules in policy config.
- [ ] Enforce during crawl and index.
- [ ] Add automated compliance tests.
- [ ] Document policy publicly.

**Acceptance Criteria**

- [ ] Disallowed sites skipped.
- [ ] Paywalls respected.
- [ ] Compliance docs published.

---

## **Initiative 6 — Trust, Auditability & Transparency**
**Depends on:** Initiatives 2, 3, 5  
**Goal:** Make outputs explainable, reproducible, and trustworthy.

---

### **Epic 1 — Evidence Linking**
**Tasks**
- [ ] Include source IDs and snippets per claim.
- [ ] Store evidence metadata in DB.
- [ ] Display clickable citations in UI.
- [ ] Reject unreferenced responses.

**Acceptance Criteria**

- [ ] All outputs cite sources.
- [ ] Click-through works.
- [ ] No claim without evidence.

---

### **Epic 2 — Bias & Credibility Scores**
**Tasks**
- [ ] Create trust dataset (domain → score).
- [ ] Integrate scores into aggregation.
- [ ] Expose bias controls in UI.
- [ ] Track fairness metrics.

**Acceptance Criteria**

- [ ] Adjusting bias slider changes ranking.
- [ ] Metrics monitored over time.
- [ ] Bias regression alerts active.

---

### **Epic 3 — Audit Trail (Signed Run Manifest)**
**Tasks**
- [ ] Define manifest schema (models, cost, retrievals, artifacts).
- [ ] Store immutable signed logs with hash verification.
- [ ] Export via API/CLI.
- [ ] Verify telemetry fields in CI.

**Acceptance Criteria**

- [ ] Signed manifests verifiable.
- [ ] Hash mismatches trigger alerts.
- [ ] Export validated.

---

### **Epic 4 — Explainability UI (“Why Included?”)**
**Tasks**
- [ ] API exposing planner decisions & memory hits.
- [ ] Visual side panel for evidence graph.
- [ ] Hover explanations with context links.
- [ ] Conduct usability sessions.

**Acceptance Criteria**

- [ ] 80 % of users understand rationale in testing.
- [ ] Performance < 1 s load.
- [ ] Evidence displayed interactively.

---

## **Initiative 7 — User Experience & Builder Interface**
**Depends on:** Initiatives 2 & 6  
**Goal:** Provide an intuitive UI to design, test, and view personalized news experiences.

---

### **Epic 1 — Conversational Builder**
**Tasks**
- [ ] Define Topic/Blueprint/View/Route schemas.
- [ ] Implement NL-to-schema diff generation.
- [ ] Add diff preview + rollback.
- [ ] Include “Test Run” mode for dry-run execution with cost/time estimation.

**Acceptance Criteria**

- [ ] Natural language edits correctly mutate schema.
- [ ] Rollback safe.
- [ ] Dry-run results accurate.

---

### **Epic 2 — Layout Studio (Web + Email)**
**Tasks**
- [ ] Create unified View schema + MJML compiler.
- [ ] Build live preview editor with component palette.
- [ ] Enforce ≤ 102 KB email size limit.
- [ ] Add cross-client rendering tests.

**Acceptance Criteria**

- [ ] Web and email share rendering.
- [ ] Email clipping warnings appear.
- [ ] Rendering validated on clients.

---

### **Epic 3 — Dashboard (Topics, Runs, Costs, Health)**
**Tasks**
- [ ] Aggregate run status/cost metrics in backend.
- [ ] Create real-time dashboard with RAG indicators.
- [ ] Add WebSocket/SSE for live progress.
- [ ] Implement alert hooks (Slack/email).

**Acceptance Criteria**

- [ ] Dashboard auto-refreshes.
- [ ] Alerts delivered reliably.
- [ ] Metrics accurate.

---

### **Epic 4 — Accessibility & Internationalization**
**Tasks**
- [ ] Perform WCAG 2.2 audit; fix contrast, focus, keyboard issues.
- [ ] Integrate `i18next` with language bundles.
- [ ] Add PWA manifest + offline caching.
- [ ] Automate accessibility testing in CI.

**Acceptance Criteria**

- [ ] UI meets WCAG AA compliance.
- [ ] i18n switching works.
- [ ] CI passes accessibility checks.

---

## **Initiative 8 — Community, Evaluation & Governance**
**Depends on:** Initiatives 1–7  
**Goal:** Ensure sustainability, transparency, and quality for an open-source community.

---

### **Epic 1 — Docs & API Site**
**Tasks**
- [ ] Deploy Docusaurus docs site (`docs.newser.*`).
- [ ] Integrate OpenAPI & Swagger snippets.
- [ ] Provide runnable examples with CI freshness check.
- [ ] Add architecture diagrams and quickstart guide.

**Acceptance Criteria**

- [ ] Docs build passes CI.
- [ ] Examples runnable.
- [ ] Docs site versioned and live.

---

### **Epic 2 — Plugin Registry**
**Tasks**
- [ ] Create DB/API models for plugin manifests.
- [ ] Build web catalog for browsing/installing plugins.
- [ ] Publish ≥ 5 example connectors/templates.
- [ ] Add manifest signature validation.

**Acceptance Criteria**

- [ ] Plugins installable and verified.
- [ ] Catalog searchable.
- [ ] Example connectors working.

---

### **Epic 3 — Evaluation Harness**
**Tasks**
- [ ] Define benchmark datasets for factuality & retrieval precision.
- [ ] Implement harness running automated benchmarks in CI.
- [ ] Gate merges on score regressions.
- [ ] Expose leaderboard in docs.

**Acceptance Criteria**

- [ ] CI blocks on quality regression.
- [ ] Evaluation scores trend upward.
- [ ] Leaderboard published.

---

### **Epic 4 — Governance & Releases**
**Tasks**
- [ ] Finalize LICENSE, CoC, CONTRIBUTING, SECURITY, CHANGELOG.
- [ ] Automate semantic versioning and GitHub Releases.
- [ ] Establish vulnerability disclosure policy.
- [ ] Create “Quickstart Sandbox” Compose setup for contributors.

**Acceptance Criteria**

- [ ] Releases automated.
- [ ] Security policy public.
- [ ] Local sandbox spins up successfully.

---

## **Feasibility Gates (Before Implementation)**

- [ ] **Sandbox runtime verified:** Docker or NSJail enforces CPU, memory, wall-clock time, file system isolation, and outbound network allowlists.
- [ ] **Core infra provisioned:** Redis Streams (or NATS), Postgres (with `pgvector` enabled), and S3-compatible object storage (e.g., MinIO) are available in dev/staging.
- [ ] **Domain & crawl policy ready:** Central allow/deny list for domains, robots.txt respect toggle, paywall policy, attribution rules configured.
- [ ] **Unified auth in place:** Shared JWT verification library wired into `api`, `worker`, `executor`, `crawler`, `memory`.
- [ ] **Observability baseline:** OpenTelemetry exporter + Prometheus running in Docker Compose; at least one Grafana dashboard committed.
- [ ] **CI baseline:** Lint + unit + integration smoke + secret scanning (Gitleaks) + a11y checks wired; CI blocks on failures.
- [ ] **Schema registry operational:** Message and plan schemas versioned and validated in CI; backward compatibility tests pass.
- [ ] **Local contributor sandbox:** One-command `docker compose up` launches a working demo (with seed data) and documented in README/Docs.
