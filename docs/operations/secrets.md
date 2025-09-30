# Secrets & Configuration Inventory

This note documents existing configuration touchpoints and outlines the approach for Initiative 1 · Epic 2.

## Current findings
- `config/config.json` ships with placeholder values for `server.jwt_secret`, `llm.providers.openai.api_key`, and database credentials (`storage.postgres.password`). They are not real secrets, but they should be sourced from the environment in all non-test contexts.
- Redis and third-party API keys rely on empty strings, which silently fall back to unauthenticated access. This can mask missing configuration during deployment.
- No .env template exists today; contributors must set environment variables manually or edit the JSON config, increasing the risk of committing accidental secrets.
- CI does not run secret-scanning (e.g. Gitleaks), so new credentials could slip into the repository unnoticed.

## Remediation plan
1. **Environment-first configuration**
   - Provide an `.env.example` enumerating every sensitive variable (JWT secret, database creds, Redis password, API keys).
   - Update documentation to instruct `cp .env.example .env` followed by editing local credentials; the `.env` file will stay Git-ignored.
   - Configure Viper (existing loader) to honour these env vars; the JSON config keeps safe defaults only.
2. **Secret scanning**
   - Add a repository-level Gitleaks configuration plus a Makefile target (`make gitleaks`).
   - Document how to wire the target into CI (GitHub Actions snippet) so pushes block on leaks.
3. **Runtime vault integration (future)**
   - Document the expectation that production deployments pull secrets from SOPS/Vault, with `.env` used only for local/dev. Implementation will follow once the team selects a backend.

The subsequent commits in this epic implement the first two steps.
