# Repository Guidelines

## Project Structure & Module Organization
- Backend lives in `cmd/` (Cobra CLI) and `internal/` (`server/`, `store/`, `agent/`, `helpers/`)—treat packages as layer boundaries.
- Shared configuration and schema sit in `config/` and `migrations/`; run `make migrate` to apply SQL changes.
- The Vite/React UI resides in `webui/`; build output in `webui/dist/` and generated artefacts in `runs/` stay out of commits unless illustrative.

## Build, Test, and Development Commands
- `make up` / `make down`: start or stop the Docker Compose stack for Postgres, Redis, API, and WebUI.
- `make serve` or `go run ./cmd/newserd`: boot the API with local env vars; pair with `make webui-dev` for full-stack work.
- `make webui-build`: run `npm ci` plus the Vite build before `make serve-all` or container images.
- `make fmt`, `make build`, `make test`: format Go, verify compilation, and execute `go test ./... -count=1` with cache disabled.

## Coding Style & Naming Conventions
- Keep Go code `gofmt`-clean; exported identifiers use `PascalCase`, locals `camelCase`, and packages stay short and lower-case.
- Co-locate Go logic with its domain (e.g., schedulers in `internal/agent`) and avoid crossing layers.
- React components live under `webui/src/components/` or `webui/src/pages/`; name components `PascalCase.tsx`, utilities `camelCase.ts`, and lean on Tailwind utilities before custom CSS.

## Testing Guidelines
- Place Go tests beside implementation files using the `*_test.go` pattern and run `make test` before pushing.
- Use Testcontainers when touching Postgres or Redis so integration coverage stays reproducible.
- Front-end updates must at least pass `npm run build`; add UI automation when adding multi-step flows and keep fixtures lightweight.

## Commit & Pull Request Guidelines
- Follow the repo’s conventional commits (`feat`, `fix`, `docs`, optional scopes like `ui` or `server`) and keep each commit focused.
- PRs should note the problem, solution, validation (`make test`, screenshots, etc.), and link tickets when available.
- Surface migrations, config updates, and manual deployment steps explicitly; avoid leaving commented-out code.

## Security & Configuration Tips
- Never commit secrets; export `OPENAI_API_KEY`, `POSTGRES_*`, `REDIS_*`, and `JWT_SECRET` in your shell or `.env` before running services.
- In Docker Compose, address dependencies by service name (`postgres`, `redis`) and revisit `config/agent_config.json` whenever you adjust agent behaviour.
