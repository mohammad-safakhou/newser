# Secrets Management

This directory contains templates and instructions for managing runtime secrets via [Mozilla SOPS](https://github.com/getsops/sops) or an external vault. All sensitive configuration should live in encrypted files checked into this folder, never in plain text.

## Quick start

1. Install SOPS (Homebrew: `brew install sops`).
2. Generate or obtain an [AGE](https://github.com/FiloSottile/age) key pair. Add the public key to `.sops.yaml`.
3. Copy the example template:
   ```bash
   cp config/secrets/app.secrets.example.yaml config/secrets/app.secrets.yaml
   sops --encrypt --in-place config/secrets/app.secrets.yaml
   mv config/secrets/app.secrets.yaml config/secrets/app.secrets.enc.yaml
   git add config/secrets/app.secrets.enc.yaml
   ```
4. Share the AGE recipient(s) with the team so others can decrypt:
   ```bash
   sops --decrypt config/secrets/app.secrets.enc.yaml > /tmp/app.secrets.yaml
   ```

The encrypted `*.enc.yaml` files can be loaded at runtime (e.g., via `sops -d` during container start) or injected into Vault/SOPS sidecars.

## Contents

- `app.secrets.example.yaml` — template covering API, worker, executor, crawler, and memory service secrets.
- `<name>.enc.yaml` — encrypted secrets committed to the repo. These match the `.sops.yaml` creation rules.

## Vault integration

If your deployment prefers Vault or AWS Secrets Manager:

- Store decrypted values (matching the keys from the template) under a single namespace, e.g. `secret/data/newser`.
- Update your deployment manifests to export those values as environment variables prior to starting each binary.
- The `.env.example` file lists every required variable so automation can stay in sync with the Go configuration loader.

## Safety checklist

- Never commit decrypted `*.yaml` files in this folder. Git ignores them by default.
- Rotate AGE keys when contributors leave the project; run `sops updatekeys` to re-encrypt.
- Keep `.env` files local-only. Use the encrypted templates for any shared configuration.
