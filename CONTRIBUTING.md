# Contributing to Newser

Thanks for your interest in helping build Newser! This document explains how to get set up locally, the workflows we
follow, and the expectations we have for code and documentation changes. By participating you agree to abide by the
[Code of Conduct](./CODE_OF_CONDUCT.md).

## Getting Started

1. **Prerequisites**
   - Go 1.22+
   - Node.js 20+ and npm (for the Vite web UI)
   - Docker Desktop (for the Postgres/Redis/testcontainers stack)
2. **Clone and bootstrap**
   ```bash
   git clone https://github.com/<org>/newser.git
   cd newser
   cp .env.example .env
   export $(grep -v '^#' .env | xargs)
   ```
3. **Run core services**
   ```bash
   make up        # Postgres, Redis, supporting services
   make serve     # API + agent pipeline
   make webui-dev # optional: Vite dev server for UI
   ```
4. **Shut everything down**
   ```bash
   make down
   ```

## Workflow

- **Issues first**: If you plan a non-trivial change, open or claim an issue so others know you are working on it. For
  roadmap-linked items, reference the relevant `Ix.Ey` identifier.
- **Branches**: Use short, descriptive branch names (`feature/topic-drafts`, `fix/api-timeouts`).
- **Commits**: Follow conventional commits, e.g. `feat(agent): add planner traces` or `fix(ui): guard null topic states`.
- **Pull Requests**: Keep PRs focused. Clearly describe the problem, solution, validation (`make test`, manual steps),
  and link any relevant issues or initiatives.

## Coding Standards

- Run `make fmt` before sending a PR. Go code must be gofmt clean.
- Keep package boundaries (`cmd/`, `internal/server`, `internal/agent`, `internal/store`, etc.) aligned with the
  architecture documented in `tasks.md`. Avoid introducing cross-layer imports.
- Prefer descriptive names (exported identifiers in PascalCase, locals in camelCase) and short package names.
- For front-end work, use PascalCase for components, camelCase for utilities, and keep styling primarily in Tailwind
  utility classes.

## Testing & Validation

- Run `make test` (`go test ./... -count=1`) before pushing. Add new tests near the code they cover (`*_test.go`).
- Front-end changes must at least pass `npm run build`. Add Vitest/Playwright coverage when introducing interactive
  flows.
- Integration tests that touch Postgres or Redis should use testcontainers to keep the suite reproducible.
- If you add migrations, document them and run `make migrate` locally before opening the PR.

## Documentation

- Update relevant docs under `docs/` or `webui/` when behavior, configuration, or APIs change.
- Keep diagrams and architecture notes in sync with code. For significant changes, include a short rationale in the PR
  and link to impacted epics.
- Governance docs (`CODE_OF_CONDUCT.md`, `SECURITY.md`, `CHANGELOG.md`) must stay current with each release.

## Release Notes

Every release must:

1. Update [`CHANGELOG.md`](./CHANGELOG.md) with user-facing highlights.
2. Follow the [release process](./docs/operations/release-process.md), including tagging and artifact publication.
3. Confirm docs and sample configurations are accurate for the new version.

## Need Help?

If you are stuck, open a discussion or reach out in the project chat. For conduct issues contact
`conduct@newser.dev`; for security disclosures follow [`SECURITY.md`](./SECURITY.md).
