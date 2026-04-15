# Agent instructions

Generic workflow for any Claude Code agent working on edvabe. Read this
once at the start of every session. It does not change between tasks.

- **Identity / architecture / conventions** → [CLAUDE.md](CLAUDE.md)
- **Current progress** → [status.md](status.md)
- **How to work** → this file

## Entry protocol

When you enter the repo cold, read in this order and stop when you have
enough context for your task:

1. **[CLAUDE.md](CLAUDE.md)** — what this project is and the golden rules
2. **[instructions.md](instructions.md)** (this file) — how to work here
3. **[status.md](status.md)** — what's done, what's in progress, what's
   next
4. **[docs/08-phase1-checklist.md](docs/08-phase1-checklist.md)** — the
   specific task entry you picked up
5. **[docs/05-architecture.md](docs/05-architecture.md)** if your task
   touches architecture (interfaces, package layout, request flow)
6. **[docs/03-api-surface.md](docs/03-api-surface.md)** if your task
   touches E2B API shapes (request/response fields, Connect-RPC details)
7. **[docs/02-e2b-internals.md](docs/02-e2b-internals.md)** if you need
   context on how real E2B is built (usually only for edge cases)
8. **[docs/07-open-questions.md](docs/07-open-questions.md)** if your
   task conflicts with or relates to an unresolved design question

Do NOT pre-read the whole `docs/` tree "just in case." Context is
precious; read by need.

## Picking a task

1. Open [status.md](status.md) and find the current phase.
2. Pick the first task marked `[ ]` (not started) under that phase.
3. Open its full description in
   [docs/08-phase1-checklist.md](docs/08-phase1-checklist.md) — each task
   has **Do** (what), **Where** (files), and **Acceptance** (how to verify).
4. Update status.md: change `[ ]` to `[~]` for that task, add a
   "picked up task N" entry at the top of the session log with the
   current date. Commit this status update **before** starting work —
   it signals the claim to any parallel agent.

If no task is unchecked in the current phase, ask the user what to do.
Do not auto-promote to the next phase.

## Session scope

- **Default**: one task per session. Commit and report back.
- **Exception**: if tasks are very small and naturally related (e.g. two
  adjacent interface definitions), you may bundle 2–3. Do not bundle
  more than 3.
- **Stop at natural boundaries.** Big tasks (Docker runtime impl, HTTP
  dispatch, template builder) deserve a whole session.
- **On a blocker**: stop, update status.md with the blocker, commit,
  report back to the user. Do not push through.

## Working on a task

1. **Understand the acceptance criterion first.** If you cannot
   articulate what "done" looks like, re-read the task description.
2. **Follow the file paths in the task description.** If it says "write
   X in `internal/foo/bar.go`," do not put it somewhere else. Paths are
   part of the architecture contract.
3. **Match the interface sketches in [docs/05-architecture.md](docs/05-architecture.md).**
   Do not drift from documented method signatures without escalating.
4. **Run the acceptance command yourself before marking done.** Don't
   trust "it compiles." Copy the exact command from the task entry.
5. **If the task description is wrong or outdated**, STOP. Update the
   checklist with the fix and commit that separately (title:
   `fix task N description: <what>`) before continuing the work.
6. **Do not add features not asked for.** If Task 7 says "implement
   Create and Destroy," do NOT also implement Pause, even if it feels
   trivial. Pause belongs to its own task/phase.

## Committing

Atomic commit per the global rule in `~/CLAUDE.md`: `git add` + `git commit`
chained with `&&` in a single bash command, with an explicit file list.

Template:

```sh
git -C /home/matej21/projects/oss/edvabe add \
    path/to/file1.go \
    path/to/file2.go \
  && git -C /home/matej21/projects/oss/edvabe commit -m "$(cat <<'EOF'
<short lowercase imperative title>

<optional body: 1-2 lines explaining WHY if non-obvious, or linking
to the task being resolved>

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

Rules:

- **NEVER** `git add -A` or `git add .` — always explicit file list.
- **NEVER** amend pushed commits.
- **NEVER** `git push --force`.
- **NEVER** `--no-verify` to skip hooks unless explicitly asked.
- **Title**: lowercase imperative, under 70 chars. Examples: `add Runtime
  interface`, `define AgentProvider`, `fix host-header parser for IPv6`.
- **Body**: optional. Include only if WHY is non-obvious. Reference the
  task number like `(phase 1 task 7)` if the commit spans exactly one
  task.

Recommended commit cadence within a session:

1. Status update when picking up a task — `claim task N`
2. One or more commits for the task implementation itself
3. Status update when done — `complete task N`

Push after each commit so parallel agents and the user see progress:

```sh
git -C /home/matej21/projects/oss/edvabe push
```

## Updating status.md

### When picking up a task

- Flip `[ ]` → `[~]` on the task entry.
- Add a line at the top of `## Session log`:
  ```
  ### <YYYY-MM-DD> — claim task N (<short title>)
  Agent: Claude Opus 4.6 (1M context)
  ```

### When completing a task

- Flip `[~]` → `[x]` on the task entry.
- Append the implementation commit hash + date to the task line, e.g.:
  ```
  - [x] **Task 2 — Runtime interface** (abc1234, 2026-04-15)
        Brief note on what was delivered if it helps future agents.
  ```
- Update the session log entry with what happened, including:
  - Files created / modified
  - Which acceptance commands passed (by name)
  - Anything surprising or non-obvious
  - New open questions added to `docs/07-open-questions.md`
  - Decisions taken during implementation that belong in the
    `## Decisions made during implementation` section

### When blocked

- Leave the task as `[~]`.
- Add a `**Blocker**:` line under the task with what and why.
- Add a "blocked on task N" note to the session log.
- Commit + push.
- Report to the user.

## Escalation — stop and ask the user if

- A golden rule in [CLAUDE.md](CLAUDE.md) seems to conflict with the task
- An architectural decision in `docs/` conflicts with what the task
  needs (check [docs/07-open-questions.md](docs/07-open-questions.md) first)
- The upstream E2B protocol behaves differently than
  [docs/03-api-surface.md](docs/03-api-surface.md) claims
- You need a new third-party Go dependency
- The acceptance criterion is unachievable as written
- You discover a design question that affects more than your current task

Do NOT:

- Silently work around golden rules
- Reinterpret task scope to make it "more complete"
- Add features not asked for
- Create new architecture decisions without user input
- Modify `docs/` files during implementation (except to fix a wrong
  task description, which is a separate commit)

## After the session — final report

Your last message to the user should include:

1. **What changed** — file paths created or modified
2. **Acceptance passed** — by name, e.g. `go vet ./... clean, go test
   ./internal/runtime/... passes`
3. **Commit hash(es)** — e.g. `def5678`
4. **Next unchecked task** — task number and title, point to
   `docs/08-phase1-checklist.md`
5. **New questions / blockers** (if any) — with links to where you
   recorded them

Keep the report tight. The user can `cat status.md` for the full picture.
