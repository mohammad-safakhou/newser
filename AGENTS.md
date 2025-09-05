AGENTS: Operating Guide for External Coding Agents

Scope
- This document defines how external development agents (e.g., Codex CLI or similar) should operate when working on this repository.
- It does NOT describe runtime agents inside the application; it’s guidance for our tooling and process only.

Goals
- Maintain clear, persistent visibility into prompts, plans, and task progress.
- Keep work scoped, reviewable, and auditable via per‑task commits.
- Avoid leaking transient agent artifacts into version control.

Ground Rules
- Always use a TODO list: Track tasks with concise descriptions and statuses (pending, in_progress, completed, blocked).
- Persist prompts and decisions: Append important prompts, plans, and status updates to local session logs.
- One task at a time: Derive a short plan (3–7 steps) for the current task before coding. Ask for clarification when ambiguous.
- Minimal surface area: Change only what is necessary for the task; call out unrelated issues separately.
- Per‑task commits: Prefer one conventional commit per finished task to preserve traceability.
- No app code scaffolding for logs: Logging is out‑of‑band; do not modify application source for agent logs.

Local Persistence (git‑ignored)
- Location: `./todos` (ignored via `.gitignore`).
- Purpose: Store session TODOs, prompts, plans, and status updates for agent runs. Never commit these files.

Recommended Layout
- Per session directory: `./todos/<date>-<session-id>/`
  - `tasks.md`: Markdown checklist actively maintained by the agent.
  - `log.jsonl`: Append‑only JSON Lines file of events (prompts, plans, status changes, notes).
  - Optional extras: `artifacts/` for scratch outputs (diffs, rendered docs), also ignored.

`tasks.md` Template
- Title: short, outcome‑focused
- Checklist:
  - [ ] Task A — definition of done …
  - [ ] Task B — definition of done …
- Notes/Constraints: assumptions, env, links
- Plan (for current task): 3–7 steps, updated as needed

`log.jsonl` Event Schema (examples)
- prompt: `{ "type": "prompt", "session": "s1", "ts": "2025-09-03T12:34:56Z", "role": "user|agent", "content": "..." }`
- plan: `{ "type": "plan", "session": "s1", "ts": "...", "tasks": [{"id":"t1","description":"...","priority":2,"depends_on":[]}] }`
- task_status: `{ "type": "task_status", "session": "s1", "ts": "...", "task_id": "t1", "status": "in_progress|completed|failed|blocked", "commit": "<hash?>", "message": "..." }`
- note: `{ "type": "note", "session": "s1", "ts": "...", "message": "context or decision" }`

Operating Procedure
1) Initialize session
   - Create `./todos/<date>-<session-id>/`.
   - Create `tasks.md` and `log.jsonl`.
2) Capture prompts
   - Append the initial problem statement to `log.jsonl` as `prompt`.
3) Plan current task
   - Produce a concise plan; append a `plan` event; add/adjust items in `tasks.md`.
4) Execute
   - Work through the plan; keep scope tight; log `task_status` transitions.
5) Commit
   - On completion, make a single commit for the task using conventional commits (see below). Record the commit hash in `task_status` and check off the item in `tasks.md`.
6) Review & next
   - Summarize results, surface follow‑ups into `tasks.md`, and proceed to the next task.

Conventional Commits (recommended)
- Format: `type(scope): summary`
  - Examples: `feat(api): add expand endpoint`, `fix(ui): handle empty highlight list`, `docs: clarify setup`
- Keep commits minimal and aligned with a single task for easier review.

Privacy & Safety
- Never store secrets in `./todos` (or anywhere in the repo).
- Respect sandboxing/network policies; document constraints in `tasks.md` as needed.
- Prefer narrow tests/build steps focused on the changes you made.

Non‑Goals
- This guide does not mandate any runtime logging or app changes to support agent operations.
- Do not commit `./todos` contents; they are local process artifacts only.

Quick Start
- Create `./todos/<date>-<session-id>/tasks.md` with your initial checklist.
- Start appending your prompts and plans to `log.jsonl`.
- Execute one task at a time, committing per task and updating both files.

