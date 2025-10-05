# Module Tasks — cmd/crawler

Roadmap references: `Ix.Ey` = Initiative/Epic from `tasks.md`, `FG` = Feasibility Gate.

- [ ] [I5.E1] Start the distributed crawler service with shard/worker configuration, robots.txt enforcement, and rate limiting controls.
- [ ] [I5.E1] Surface CLI options for dedup windows, refresh intervals, and crawl budgets per domain or topic.
- [ ] [I5.E1] Emit structured crawl metrics (skip/fetch decisions, politeness) and integrate with OTEL exporters.
- [ ] [I5.E2] Provide SimHash/MinHash toggle flags and canonical URL normalization settings.
- [ ] [I5.E4] Load crawl policy configs (disallow/paywall/attribution rules) and fail fast if policy validation fails.
- [ ] [FG] Ensure startup verifies Redis/queue connectivity and registers schema versions for crawl messages.
