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
      `go.mod`, `cmd/edvabe/main.go` CLI with serve/doctor/build-image/pull-base/version
      stubs, `Makefile`, `.gitignore`, `README.md`. `make build && ./bin/edvabe version`
      prints correctly; `go vet ./...` clean.
      (Subcommand renamed fetch-envd → pull-base in task 4.)
- [x] **Task 2 — Runtime interface** (b396ca4, 2026-04-15)
      `internal/runtime/runtime.go` defines `Runtime` + `CreateRequest`,
      `SandboxHandle`, `Stats`, `BuildRequest` per docs/05-architecture.md.
      `internal/runtime/noop` is an in-memory impl with a `HasImage` /
      `IsPaused` test helper; used by higher-layer unit tests.
- [x] **Task 3 — AgentProvider interface** (e12d6cd, 2026-04-15)
      `internal/agent/agent.go` — `AgentProvider` interface + `InitConfig`
      + `VolumeMount`. Imports `internal/runtime` for `EnsureImage`'s
      Runtime arg. No impls yet; upstream impl lands in task 4/5.
- [x] **Task 4 — Pin e2bdev/base image** (00e48df, 2026-04-15)
      `internal/agent/upstream/image.go` with `DefaultEnvdVersion`,
      `BaseImageRepo`, `BaseImageDigest` consts + `PullBase(ctx)` helper
      shelling out to `docker pull`. `pull-base` subcommand replaces the
      old `fetch-envd` stub. Scope changed mid-task — see session log
      and decisions section.
- [x] **Task 5 — Tag e2bdev/base as edvabe/base** (16142e7, 2026-04-15)
      `EnsureBaseImage(ctx, tag)` in `internal/agent/upstream/image.go`
      wraps `PullBase` + `docker tag`. `build-image` subcommand wires
      it with default `--tag edvabe/base:latest`. `--force` accepted
      but no-op (pulls by digest are idempotent).
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

### 2026-04-15 — complete task 5 (tag e2bdev/base)

Agent: Claude Opus 4.6 (1M context)

- Added `EnsureBaseImage(ctx, tag)` to `internal/agent/upstream/image.go`:
  `PullBase` followed by `docker tag <BaseImageRef> <tag>`. Idempotent.
- Wired `build-image` subcommand in `cmd/edvabe/main.go`: default
  `--tag edvabe/base:latest`, `--force` accepted but a no-op (pulls by
  digest are already idempotent — documented in the flag usage).
- Acceptance: `go vet ./...` + `go build ./...` clean;
  `docker rmi edvabe/base:latest` (clean slate);
  `go run ./cmd/edvabe build-image` prints the tag mapping;
  `docker images edvabe/base` shows `edvabe/base:latest` pointing at
  the same image ID (`1565260ff3fe`) that task 4 pulled.
- The task description's additional smoke step (run container, curl
  `/health`) belongs to task 6 — skipped here.
- Commits: `74b5fd0` (claim), `16142e7` (implementation).
- No new open questions.

### 2026-04-15 — complete task 4 (pin e2bdev/base image)

Agent: Claude Opus 4.6 (1M context)

Task scope changed mid-flight. Original plan was to fetch a prebuilt
envd binary from `e2b-dev/infra` GitHub releases, sha256-verify, cache
in `~/.cache/edvabe/envd/<version>/`. Investigation showed:

- `e2b-dev/infra` has 37 releases, all with zero assets. envd is
  uploaded to E2B's private GCP/AWS buckets (see `packages/envd/Makefile`
  `upload` target).
- `e2bdev/base` is published on Docker Hub multi-arch (amd64 + arm64),
  ~470 MB compressed, last pushed 2026-02-25. It already contains envd
  baked in.

Escalated to user, agreed to switch to Q2 option (D) — consume
`e2bdev/base` directly. This resolves **Q2** and makes task 5 a thin
retag.

- Rewrote tasks 4 and 5 in `docs/08-phase1-checklist.md`; marked Q2
  resolved in `docs/07-open-questions.md`; updated Q14 upgrade flow;
  renamed `fetch-envd` → `pull-base` across CLAUDE.md, docs, main.go,
  and the task 1 historical note. Commit: `df593cd`.
- Implementation: `internal/agent/upstream/image.go` with pinned
  multi-arch index digest
  `sha256:11349f027b11281645fd8b7874e94053681a0d374508067c16bf15b00e1161b2`,
  verified via registry HEAD on 2026-04-15. `PullBase(ctx)` shells out
  to `docker pull` (os/exec, not the Docker SDK — SDK lands in task 7).
  `cmd/edvabe/main.go` wires `pull-base`. Commit: `00e48df`.
- Acceptance: `go vet ./...` + `go build ./...` clean;
  `go run ./cmd/edvabe pull-base` prints the digest-pinned ref;
  `docker image inspect <ref>` returns a non-empty local ID
  (`sha256:1565260ff3fe...`, amd64, 1.28 GB unpacked).
- Commits this session: `62a1028` (claim), `df593cd` (fix task 4/5
  descriptions), `00e48df` (implementation).
- No new open questions. Scope change recorded in Decisions section.

### 2026-04-15 — complete task 3 (AgentProvider interface)

Agent: Claude Opus 4.6 (1M context)

- Added `internal/agent/agent.go` with `AgentProvider` interface plus
  `InitConfig` and `VolumeMount` structs. Method signatures match
  [docs/05-architecture.md](docs/05-architecture.md#agent-provider-interface);
  `VolumeMount` kept minimal (`Name`, `MountPath`) since Phase 1 passes
  an empty list — Phase 4 will extend as needed.
- `EnsureImage` takes `runtime.Runtime` so the upstream impl (task 5)
  can call `BuildImage` without a cross-package back-reference. No
  import cycle: agent → runtime only.
- Acceptance: `go vet ./...` clean, `go build ./...` clean.
- Commits: `75e8c3f` (claim), `e12d6cd` (implementation).
- No new open questions.

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

- **2026-04-15** — Consume `e2bdev/base` from Docker Hub unchanged
  instead of fetching/building envd. Pin by OCI image index digest
  (multi-arch), use `os/exec docker pull` until task 7 adds the Docker
  SDK (task 4). Why: E2B doesn't publish envd binaries to GitHub
  releases, host-side Go toolchain is ruled out (Q2), and `e2bdev/base`
  is already maintained and multi-arch. See Q2 resolution for context.

## Session hygiene

- Every agent session starts by reading [instructions.md](instructions.md).
- Every task picked up results in at least two commits: `claim task N`
  (status update) and `complete task N` (status update). Implementation
  commits go in between.
- When in doubt, stop and ask the user. Do not improvise on architecture.
