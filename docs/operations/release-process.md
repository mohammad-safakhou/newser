# Release Process

This guide describes how to create an official Newser release, from preparation to post-publication follow-up. Treat it
as a living checklist—update it when the tooling or workflow evolves.

## 1. Plan the Release

- Ensure the target work is tracked (issue, milestone, or roadmap reference such as `I8.E4`).
- Confirm the changelog accurately summarizes user-facing changes.
- Decide on the next semantic version (`MAJOR.MINOR.PATCH`). Favor release candidates (`-rc.1`) if testing needs
  community validation.

## 2. Create a Release Branch

```bash
git checkout main
git pull origin main
git switch -c release/vX.Y.Z
```

Update metadata on this branch:

- Bump versions in binaries or Docker images if required.
- Update [`CHANGELOG.md`](../../CHANGELOG.md) under `## [Unreleased]`, promoting entries into a new `## [vX.Y.Z]` section.
- Regenerate any API schemas or docs impacted by the release.

Commit with a conventional message, e.g. `chore(release): prepare vX.Y.Z`.

## 3. Run the Validation Suite

- `make fmt`
- `make test`
- `npm ci && npm run build` inside `webui/`
- `make webui-build` to produce the production bundle (optional if UI changes landed)
- `make build` to verify all Go binaries compile
- `make migrate` against a disposable database when migrations changed

Fix any failures before proceeding. Document manual validation (UI smoke tests, tool execution flows) in the PR.

## 4. Tag the Release

Once the branch is green:

```bash
git commit --allow-empty -m "chore(release): cut vX.Y.Z"
git tag -s vX.Y.Z -m "Newser vX.Y.Z"
git push origin release/vX.Y.Z
git push origin vX.Y.Z
```

If signing keys are unavailable, use an annotated tag (`git tag -a`).

## 5. Publish Artifacts

1. Draft a GitHub Release for `vX.Y.Z`.
2. Attach build artifacts:
   - `dist/` bundles from `webui/` (zip/tarball)
   - Pre-built binaries if generated (`make build` output)
   - Container images pushed to the registry, tagged `newser:<version>`
3. Paste changelog highlights, upgrade notes, and any breaking changes into the release notes.

## 6. Post-Release Tasks

- Merge the release branch back to `main` (`git merge --ff-only` if possible) and delete the branch.
- Add a fresh `## [Unreleased]` section to the changelog if it was promoted.
- Update documentation that references the "latest" version numbers (Docker tags, sample configs).
- Create follow-up issues for deferred work or known gaps.
- Announce the release on the project channels.

## 7. Hotfix Procedure

For urgent fixes against the latest release:

1. Branch from the tagged release (`git switch -c hotfix/vX.Y.(Z+1) vX.Y.Z`).
2. Apply the minimal fix, update tests, and amend the changelog with a `## [vX.Y.(Z+1)]` entry under `### Fixed`.
3. Repeat the validation, tagging, and publishing steps above.

Keeping this process consistent ensures downstream users can trust the cadence, artifacts, and documentation for each
Newser release.
