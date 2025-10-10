# Secrets & Configuration Inventory

This note documents the current configuration touchpoints and how to manage secrets for Initiative 1 · Epic 2.

## Repository layout

- `.env.example` — exhaustive template for all `NEWSER_*` and legacy env vars consumed by Go binaries and docker-compose.
- `config/secrets/` — SOPS/Vault integration assets (`README.md`, example template, encrypted bundles).
- `.sops.yaml` — default SOPS creation rules targeting `config/secrets/*.enc.yaml` files.

## Local workflow

1. Copy `.env.example` to `.env`, customise values, and export them before running any services:
   ```bash
   cp .env.example .env
   export $(grep -v '^#' .env | xargs)
   ```
2. Generate encrypted secrets using SOPS. The helper targets wrap common commands:
   ```bash
   make secrets-edit   # opens config/secrets/app.secrets.enc.yaml with sops
   make secrets-decrypt
   ```
   Decrypted output is written to `config/secrets/app.secrets.tmp.yaml` and is ignored by Git.

## Production guidance

- Use the encrypted templates as source-of-truth when syncing into Vault, AWS Secrets Manager, or Kubernetes secrets.
- If Vault is preferred, mirror the keys from `config/secrets/app.secrets.example.yaml` and inject them via environment variables before services start.
- Rotate AGE keys on personnel changes and run `sops updatekeys` to re-encrypt existing bundles.

## Automation checklist

- `make gitleaks` is available to run secret scanning locally or in CI.
- Docker Compose services expect secrets via `NEWSER_*` env vars; keep `.env` (or decrypted SOPS output) in sync with infrastructure as code.
- Monitor telemetry by exporting `NEWSER_TELEMETRY_OTLP_ENDPOINT` and metrics ports per service (base port defined in `.env.example`).
