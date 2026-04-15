# edvabe project status

Living document. Agents update this when picking up, completing, or
blocking on tasks. See [instructions.md](instructions.md) for the
update protocol.

## Current phase

**Phase 1 — "Single binary runs everything"**
Full task definitions: [docs/08-phase1-checklist.md](docs/08-phase1-checklist.md)

## Phase 1 tasks

Legend: `[ ]` not started · `[~]` in progress · `[x]` done

- [x] **Task 1 — Project skeleton** (f8a8070, 2026-04-15)
      `go.mod`, `cmd/edvabe/main.go` CLI with serve/doctor/build-image/fetch-envd/version
      stubs, `Makefile`, `.gitignore`, `README.md`. `make build && ./bin/edvabe version`
      prints correctly; `go vet ./...` clean.
- [x] **Task 2 — Runtime interface** (b396ca4, 2026-04-15)
      `internal/runtime/runtime.go` defines `Runtime` + `CreateRequest`,
      `SandboxHandle`, `Stats`, `BuildRequest` per docs/05-architecture.md.
      `internal/runtime/noop` is an in-memory impl with a `HasImage` /
      `IsPaused` test helper; used by higher-layer unit tests.
- [ ] **Task 3 — AgentProvider interface**
- [ ] **Task 4 — Upstream envd: binary fetcher**
- [ ] **Task 5 — Upstream envd: base image builder**
- [ ] **Task 6 — envd-in-Docker smoke test** (gates open question Q3)
- [ ] **Task 7 — Docker runtime implementation**
- [ ] **Task 8 — Sandbox manager**
- [ ] **Task 9 — Dispatch + reverse proxy**
- [ ] **Task 10 — Control plane: health + create + get**
- [ ] **Task 11 — Control plane: list + delete + timeout + connect**
- [ ] **Task 12 — Python SDK E2E test**
- [ ] **Task 13 — TypeScript SDK E2E test**
- [ ] **Task 14 — Doctor subcommand**
- [ ] **Task 15 — Tag v0.1.0**

## Phase 2+ (not yet active)

See [docs/06-phases.md](docs/06-phases.md) for Phases 2–5 scope. No task
breakdown yet — create a `docs/09-phase2-checklist.md` (or similar) when
Phase 1 is complete.

## Session log

Newest first. Keep entries tight. Reference commit hashes so future
agents can `git show` the actual changes.

### 2026-04-15 — complete task 2 (Runtime interface)

Agent: Claude Opus 4.6 (1M context)

- Added `internal/runtime/runtime.go` with `Runtime` interface and the
  `CreateRequest`, `SandboxHandle`, `Stats`, `BuildRequest` shared types.
  Signatures match [docs/05-architecture.md](docs/05-architecture.md#runtime-interface).
- Added `internal/runtime/noop/noop.go` — in-memory impl that also
  exposes `HasImage` and `IsPaused` helpers so higher layers can assert
  Commit/BuildImage/Pause plumbing in their tests without needing a real
  runtime.
- Added `internal/runtime/noop/noop_test.go` — covers create/inspect
  (via `AgentEndpoint` + `Stats`) /destroy, duplicate-create rejection,
  Pause/Unpause, Commit + BuildImage, and missing-ID error paths.
- Acceptance: `go vet ./...` clean, `go build ./...` clean,
  `go test ./internal/runtime/...` passes (noop package; runtime pkg has
  no tests — it's pure type definitions).
- Commits: `49e3783` (claim), `b396ca4` (implementation).
- No new open questions. No deviations from `docs/05-architecture.md`.

### 2026-04-15 — initial design, docs, and Phase 1 Task 1

Agent: Claude Opus 4.6 (1M context)

- Researched E2B API surface, internal architecture, and Firecracker vs
  Docker tradeoffs via three parallel agents against the upstream
  `e2b-dev/infra`, `e2b-dev/e2b`, and `e2b-dev/code-interpreter` repos.
- Wrote design docs (`docs/README.md`, `docs/01-overview.md`,
  `docs/02-e2b-internals.md`, `docs/03-api-surface.md`,
  `docs/04-runtime-decision.md`, `docs/05-architecture.md`,
  `docs/06-phases.md`, `docs/07-open-questions.md`,
  `docs/08-phase1-checklist.md`).
- Key architectural decisions recorded in docs:
  - Docker runtime, not Firecracker ([04](docs/04-runtime-decision.md))
  - Reuse upstream envd binary in Phase 1, not reimplement
    ([07 Q1](docs/07-open-questions.md))
  - `Runtime` and `AgentProvider` are separate pluggable interfaces
    ([05](docs/05-architecture.md))
  - edvabe itself does NOT implement Connect-RPC; reverse-proxies envd
    ([01](docs/01-overview.md), [05](docs/05-architecture.md))
- Wrote `CLAUDE.md` agent brief at the repo root.
- Completed **Task 1** (project skeleton): `go.mod`, `cmd/edvabe/main.go`,
  `Makefile`, `.gitignore`, `README.md`. Commit `f8a8070`.
- Created public GitHub repo at https://github.com/contember/edvabe.

### 2026-04-15 — agent workflow files

Agent: Claude Opus 4.6 (1M context)

- Split the agent workflow from `CLAUDE.md` into three files:
  - `CLAUDE.md` — project identity, golden rules, commands, references
  - `instructions.md` — generic agent workflow (entry protocol, task
    picking, commits, status updates, escalation)
  - `status.md` (this file) — living progress tracker
- Updated `docs/08-phase1-checklist.md` to point at `status.md` for
  tracking instead of its own completed-tasks section.
- Added `instructions.md` and `status.md` pointers to `docs/README.md`.

## Open blockers

None.

## Decisions made during implementation

Entries added here are decisions taken while writing code that aren't
already captured in `docs/`. Format: `- **<date>** — <decision> (task N).
Why: <reason>.`

*(none yet)*

## Session hygiene

- Every agent session starts by reading [instructions.md](instructions.md).
- Every task picked up results in at least two commits: `claim task N`
  (status update) and `complete task N` (status update). Implementation
  commits go in between.
- When in doubt, stop and ask the user. Do not improvise on architecture.
