# Newser

Getting personalized news all the time, not as a SPAM :)

## Overview

Newser is an intelligent news aggregation system that delivers personalized news summaries based on user-defined topics.
The system uses AI-powered topic analysis and conversation management to create customized news feeds that are delivered
on a schedule, ensuring users get relevant information without spam.

## Quick start (API + WebUI)

- Dependencies: Docker (for Postgres/Redis via compose), Go 1.22+, Node/npm if building WebUI locally
- Start infra: `make up`
- Env (example):
```bash
cp .env.example .env
# edit .env with your secrets, then export for docker-compose or go run
export $(grep -v '^#' .env | xargs)
```

- Update sandbox policy as needed in `config/security_policy.yaml` before running executor tasks. Default provider is Docker with network disabled.
- Build WebUI (optional, requires npm):
```bash
make webui-build
```
- Start observability stack (Prometheus, Tempo, Grafana) alongside services:
```bash
docker compose up otel-collector tempo prometheus grafana -d
```

- Run server (serves API and `webui/dist` if present):
```bash
make serve
# or build UI then serve in one: make serve-all
```

### Docker Compose

The repo ships with a multi-service compose file that runs Postgres, Redis, the API server, and a separate Nginx-based WebUI container.

- Start everything: `docker compose up -d`
- UI: http://localhost:3000 (proxies `/api` to the API container)
- API: http://localhost:10001
- Grafana: http://localhost:3001 (default credentials admin/admin)
- Prometheus: http://localhost:9090
- Tempo OTLP (traces): gRPC on `tempo:4317`
- Capability registry CLI: `./tools list` or `./tools publish --file tool.json`

Notes:
- Chat/assist features require `OPENAI_API_KEY`. The API fails fast on startup if the key is missing.
- In Docker Compose, never use `localhost`/`127.0.0.1` for cross‑container services. Use the service name (e.g. `redis`, `postgres`) via env `NEWSER_REDIS_HOST=redis`, `POSTGRES_HOST=postgres`.
- To enable full features via compose, export `OPENAI_API_KEY` in your shell before `docker compose up` or inject it into the `app` service environment.

API docs: GET /api/docs, OpenAPI: GET /api/openapi.yaml
Health: GET /healthz, Metrics: GET /metrics

## Features
- **Web User Interface**: Integrated browser-based interface for managing topics and generating news.

## Web User Interface

Minimal React app is under `webui/`. When `webui/dist` exists, the server serves it at `/` with index fallback.

## Architecture (high level)

- Echo API with JWT auth (cookie or Bearer)
- Postgres primary DB (migrations via golang-migrate)
- Redis for scheduler locks/sessions
- Agent pipeline (planner/research/analysis/synthesis/conflict/highlights) with real sources and OpenAI (gpt-5 family)

## Getting Started (API only)

- Install Go deps: `go mod tidy`
- Export DB envs (see Quick start) and run: `go run ./cmd/newserd`

## Tests

- Integration tests (spin up ephemeral Postgres via testcontainers):
```bash
go test ./examples/integration -v
```

## Configuration

- Primary agent config: `config/agent_config.json` (overridden by env)
- Server reads env and also maps agent Postgres/Redis settings to env when available
- Key envs:
  - `OPENAI_API_KEY`
  - `POSTGRES_HOST/PORT/USER/PASSWORD/DB` or `DATABASE_URL`
  - `REDIS_HOST/PORT/PASSWORD`
  - `JWT_SECRET`

## License

MIT

## Contributing & Governance

- See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for development workflow and testing requirements.
- Review the [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md) before participating in the community.
- Report security issues privately following [`SECURITY.md`](./SECURITY.md).
- Track noteworthy changes in [`CHANGELOG.md`](./CHANGELOG.md).
- Follow the documented [release process](./docs/operations/release-process.md) when cutting new versions.
- Manage shared secrets with SOPS (`make secrets-edit`); see [`docs/operations/secrets.md`](./docs/operations/secrets.md).
