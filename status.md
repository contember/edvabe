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
- [x] **Task 5 — Build edvabe/base (multi-stage envd layer)**
      (83a973b, 2026-04-15, reworked from 16142e7)
      `assets/Dockerfile.base` + `assets/assets.go` (go:embed) + rewritten
      `upstream.EnsureBaseImage` that pipes the Dockerfile to
      `docker build -` with `--build-arg ENVD_SHA`. Builder stage
      `golang:1.25.4-bookworm` clones infra, deletes `go.work` to escape
      the workspace Go pin, builds `packages/envd`. Final stage starts
      from the pinned `e2bdev/base` and copies envd to `/usr/bin/envd`
      with `CMD ["-isnotfc"]`. Verified: `docker run edvabe/base:latest`
      + `curl /health` → 204. envd logs three cgroup-creation warnings,
      falls back to no-op cgroup manager (Q3 finding — full write-up in
      task 6).
- [x] **Task 6 — envd-in-Docker smoke test** (237e615, 2026-04-15)
      `test/smoke/envd_in_docker.sh` exercises /health, /init, and one
      `process.Process/Start` Connect-RPC call (`echo hello`). Resolves
      Q3 — envd runs cleanly with `-isnotfc` in plain Docker, no
      special caps or bind mounts needed. Also fixed a latent bug:
      `Dockerfile.base` now creates the `user` account E2B SDKs
      default to (e2bdev/base ships only root).
- [x] **Task 7 — Docker runtime implementation** (4c29a57, 2026-04-15)
      `internal/runtime/docker/` package with Create/Destroy/Stats/
      BuildImage/AgentEndpoint against the local Docker socket via
      `github.com/moby/moby/client` v0.4.0. Socket discovery honors
      `DOCKER_HOST` then probes Docker Desktop, Colima, OrbStack,
      Podman. Sandboxes use the sandbox ID verbatim as the container
      name plus an `edvabe.sandbox.id` label. Pause/Unpause/Commit
      return phase-4 stubs. Integration tests behind
      `-tags=integration`.
- [x] **Task 8 — Sandbox manager** (e7da54c, 2026-04-15)
      `internal/sandbox/` package: `Sandbox` + `State` types,
      `Manager` with `Create/Get/List/Destroy/Connect/SetTimeout/
      EnforceTimeouts/Run`, ID and token minting (`isb_` / `ea_` /
      `ta_` prefixes from `crypto/rand`), injectable `Clock`
      interface for deterministic timeout tests. Unit tests wire it
      up against `internal/runtime/noop` + a local stub agent;
      `-race` clean.
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

### 2026-04-15 — complete task 8 (sandbox manager)

Agent: Claude Opus 4.6 (1M context)

- Shape of the `Sandbox` struct matches the
  `docs/05-architecture.md#sandbox-state-and-persistence` sketch but
  drops `Lifecycle` and `NetworkConfig` for Phase 1 — task 10 will add
  them back when the HTTP handler needs to serialize `SandboxDetail`.
  `mu` is held on the Manager, not on each sandbox — a single lock
  keeps the code small and the contention is negligible for the
  ~10-50 sandbox working set.
- ID/token generation is `crypto/rand` + stdlib encodings: sandbox ID
  is `isb_` + 16 chars base32 (10 random bytes, no padding); envd +
  traffic tokens are `ea_`/`ta_` + 22 chars base64url (16 random
  bytes, no padding). No `oklog/ulid` or `go-ulid` dependency — the
  SDKs only match on the prefix, not the ULID structure. 1000-sample
  uniqueness check is part of the unit suite.
- Clock is a local `Clock` interface (just `Now()`) rather than
  pulling in `clockwork`. `realClock` wraps `time.Now`, test file
  defines a `fakeClock` with `Advance(d)` for deterministic expiry
  tests. Watchdog is a separate `Run(ctx, interval)` that drives
  `EnforceTimeouts` on a ticker — tests skip `Run` entirely and
  call `EnforceTimeouts` directly after advancing the fake clock.
- `Create` runs the full flow even in the Phase 1 skeleton:
  `runtime.Create` → `agent.Ping` → `agent.InitAgent`. The stub
  agent counts Ping/Init calls so the test asserts the handshake
  fires once. On Ping/Init failure the runtime container is
  force-destroyed before returning so nothing leaks. `Metadata`
  and `EnvVars` are cloned into the Sandbox so later caller-side
  mutations don't race with reads — verified by
  `TestCreateClonesInputMaps`.
- `Destroy` removes from the registry **first**, then calls
  `runtime.Destroy`. This means a runtime-side failure propagates
  the error to the caller but the Manager's map is already
  coherent; future reconnect flows can reap orphans via the
  `edvabe.sandbox.id` label stamped in task 7.
- `SetTimeout` and `Connect` both return `ErrExpired` when a
  sandbox has lapsed its TTL but hasn't been reaped yet — covered
  by `TestSetTimeoutOnExpired` and `TestConnectExpired`. The
  expiry check uses `!ExpiresAt.After(now)` so `now == ExpiresAt`
  is expired (reaped). `ErrNotFound` and `ErrExpired` are the two
  sentinel errors handlers will match on.
- Files:
  - `internal/sandbox/sandbox.go` — `Sandbox` struct + `State` enum.
  - `internal/sandbox/idgen.go` — `NewSandboxID/NewEnvdToken/
    NewTrafficToken`.
  - `internal/sandbox/manager.go` — `Manager`, `Options`, `Clock`,
    `CreateOptions`, the seven lifecycle methods + `Run` watchdog,
    sentinel errors, `DefaultImage/DefaultDomain/DefaultTimeout/
    WatchdogInterval` consts.
  - `internal/sandbox/manager_test.go` — 16 unit tests: constructor
    validation, Create/Get/List, map cloning, Destroy, timeout reap
    (single + multi), SetTimeout extension/missing/expired, Connect
    extension/missing/expired, default timeout, ID prefixes +
    uniqueness, token prefixes. Built against `internal/runtime/noop`
    and a local `stubAgent`.
- Acceptance:
  - `go vet ./...` clean, `go build ./...` clean.
  - `go test ./internal/sandbox/...` all 16 pass.
  - `go test -race ./internal/sandbox/...` clean (watchdog
    concurrency path covered).
  - `go test ./...` green across the whole unit suite.
- Commits: `3fd97e6` (claim), `e7da54c` (implementation).
- No new open questions.

### 2026-04-15 — claim task 8 (sandbox manager)

Agent: Claude Opus 4.6 (1M context)

### 2026-04-15 — complete task 7 (Docker runtime implementation)

Agent: Claude Opus 4.6 (1M context)

- Hit an SDK-split surprise: `github.com/docker/docker/client` v0.4.0
  is a tombstone redirecting to `github.com/moby/moby/client`. The
  classic monolithic `github.com/docker/docker v28.5.2+incompatible`
  conflicts with the new split `github.com/moby/moby/api v1.54.1`.
  Switched all imports to `github.com/moby/moby/{client,api/...}` and
  `github.com/moby/go-archive` for the build context tar. Clean
  `go mod tidy` with those three direct deps.
- The new client API is a significant rewrite vs the pre-v28 SDK:
  - `ContainerCreate(ctx, client.ContainerCreateOptions{Config, HostConfig, Name})`
    returns `ContainerCreateResult{ID}`, not separate args.
  - `ContainerStart` / `ContainerRemove` / `ContainerInspect` all
    return `(Result, error)` with per-method option structs in the
    client package (not api/types/container).
  - `ContainerInspect` returns `client.ContainerInspectResult` which
    wraps `container.InspectResponse`.
  - `ContainerStats(ctx, id, client.ContainerStatsOptions)` replaces
    the old `ContainerStatsOneShot`. `IncludePreviousSample: true`
    gets the prior sample for CPU delta.
  - `ImageBuild` takes `client.ImageBuildOptions` (not build.ImageBuildOptions).
  - `network.EndpointSettings.IPAddress` is `netip.Addr`, not string —
    use `.IsValid()` + `.String()`.
  - `Ping(ctx, client.PingOptions{})` now takes options.
  - `ImageInspect(ctx, ref)` is a variadic-options call, not a
    struct-options call like most other methods.
- Files:
  - `internal/runtime/docker/runtime.go` — `Runtime` struct, `New`,
    `DiscoverHost` (DOCKER_HOST → /var/run/docker.sock → Colima →
    OrbStack → Podman), `Name`, `Host`, `Close`, endpoint cache.
  - `internal/runtime/docker/create.go` — Create with label stamping,
    env/metadata/bind-mount passthrough, best-effort cleanup on any
    mid-create error.
  - `internal/runtime/docker/destroy.go` — Destroy + Pause/Unpause
    phase-4 stubs.
  - `internal/runtime/docker/stats.go` — Stats via one-shot request
    with pre-sample for CPU%; local `statsDoc` struct to avoid the
    SDK's internal stats-type churn.
  - `internal/runtime/docker/build.go` — BuildImage via moby/go-archive
    TarWithOptions + ImageBuild, drains the build output. Commit
    phase-4 stub.
  - `internal/runtime/docker/endpoint.go` — AgentEndpoint with
    cache-first lookup, falls back to live ContainerInspect so
    restarted edvabe can still proxy. `extractBridgeIP` prefers the
    default `bridge` network then any attached network.
  - `internal/runtime/docker/runtime_test.go` — unit tests for
    DOCKER_HOST path and phase-4 stub returns (no Docker needed).
  - `internal/runtime/docker/runtime_integration_test.go` — gated
    `//go:build integration`, uses `edvabe/base:latest` because
    alpine:latest exits immediately and loses its bridge IP before
    inspect. Full Create → AgentEndpoint → Stats → Destroy cycle,
    label assertions, required-field rejections.
- Acceptance:
  - `go vet ./...` clean, `go build ./...` clean.
  - `go test ./...` passes (unit suite, no Docker required).
  - `go test -tags=integration ./internal/runtime/docker/...` passes
    against the host daemon: TestDockerRuntimeCreateInspectDestroy +
    CreateRequiresID + CreateRequiresImage, 1.3s total.
- Scope held: Pause/Unpause/Commit are phase-4 stubs that return a
  "not implemented" error. `upstream.PullBase` was left shelling out
  to `docker pull` — migrating it is not in task 7 scope.
- Commits: `b9fca63` (claim), `4c29a57` (implementation).
- No new open questions.

### 2026-04-15 — claim task 7 (Docker runtime implementation)

Agent: Claude Opus 4.6 (1M context)

### 2026-04-15 — complete task 6 (envd-in-Docker smoke test)

Agent: Claude Opus 4.6 (1M context)

- Studied envd's `/init` shape (`packages/envd/spec/envd.yaml`) and
  `process.proto` to craft correct request bodies. Connect-RPC
  server-stream framing: 1 byte flags + 4 bytes BE length + JSON body;
  end-of-stream is flag `0x02` with empty trailer.
- Manual bring-up iteration uncovered that `e2bdev/base` ships only
  root. edvabe's `InitConfig` defaults to `DefaultUser="user"` /
  `DefaultWorkdir="/home/user"` (docs/05-architecture.md), so
  `process.Process/Start` failed with `invalid default user: 'user'`
  until I added `RUN useradd -m -s /bin/bash user` +
  passwordless-sudo line to `assets/Dockerfile.base` and rebuilt.
  This is a latent Phase 1 blocker fix, not scope creep — the
  sandbox manager in task 8 will need it.
- After the Dockerfile fix, full e2e works:
  - `GET /health` → 204
  - `POST /init` (with `ea_smoketoken`, `defaultUser=user`) → 204
  - `POST /process.Process/Start` with `echo hello` →
    `StartEvent{pid}` → `DataEvent{stdout:"aGVsbG8K"}` →
    `EndEvent{exited:true, status:"exit status 0"}` → EOS (`0x02`).
- `test/smoke/envd_in_docker.sh` scripts all five steps (preflight,
  start, /init, RPC, cleanup). Bash + inline python3 for Connect
  framing; `trap cleanup EXIT` so failures never leak containers.
- Q3 resolved in full in `docs/07-open-questions.md`:
  - cgroup warnings at boot are benign no-op fallbacks.
  - `-isnotfc` short-circuits MMDS — no log spam, no impact.
  - `/init` timestamp check works at `now` without `CAP_SYS_TIME`.
  - socat port forwarder and PTY cgroups flagged as **known gaps**
    for Phase 2+ (not Phase 1 blockers).
- Commits: `ead4693` (reclaim), `237e615` (implementation).
- Task 7 (Docker runtime implementation) is next and is ungated —
  Q3 was the blocking question.

### 2026-04-15 — reclaim task 6 (envd-in-Docker smoke test)

Agent: Claude Opus 4.6 (1M context)

### 2026-04-15 — complete task 5 rework (multi-stage envd build)

Agent: Claude Opus 4.6 (1M context)

- Pinned `EnvdSourceSHA = "d9063bd8cc70b5ce653e9f7cd4ede0f1e3de0fef"`
  (HEAD of `e2b-dev/infra` tag 2026.15, resolved via
  `gh api repos/e2b-dev/infra/git/refs/tags/2026.15`).
- Hit two issues iterating:
  - `golang:1.24-bookworm` builder couldn't satisfy `go.work`'s
    `go 1.25.4` requirement with `GOTOOLCHAIN=local`. Bumped to
    `golang:1.25.4-bookworm` and verified it's a real Docker Hub tag.
  - envd references sibling `packages/shared` via a `replace` directive
    in its own `go.mod` — fine, but the root `go.work` adds workspace
    constraints we don't need. `rm -f go.work go.work.sum` before cd'ing
    into `packages/envd` sidesteps it.
- First successful build took 42s wall (golang image pull + git clone +
  go mod download + go build). Cached re-runs finish in <2s.
- Smoke check: `docker run edvabe/base:latest` → `curl /health` → 204.
  envd emits three "failed to create cgroup*" warnings and falls back
  to a no-op cgroup manager — benign, Q3 write-up belongs to task 6.
- Fixed misleading "tagged ..." message in `build-image` subcommand;
  now prints `built <tag> (envd @ <sha>)`. `--force` documented as a
  no-op (Docker's build cache handles re-runs).
- Commits this session pivot: `6a8e3df` (fix task 5 description),
  `83a973b` (implementation).
- Task 6 stays `[ ]` — will be reclaimed next.

### 2026-04-15 — reopen task 5, defer task 6

Agent: Claude Opus 4.6 (1M context)

While kicking off task 6 I inspected `edvabe/base:latest` (which is
just a retag of `e2bdev/base`) and found it does NOT contain envd.
`find / -name envd -type f` in the container returns nothing; the
default CMD is `python3`. `e2bdev/code-interpreter:latest` is the
same — neither public image ships envd. E2B's orchestrator injects
envd into sandbox images outside of what they publish to Docker Hub.

Task 6's health check cannot possibly pass against the current
`edvabe/base:latest`. Escalated to user. Chose plan **B1**: reopen
task 5 and redo it as a multi-stage `docker build` that compiles envd
from source at a pinned `e2b-dev/infra` commit and layers it onto the
same pinned `e2bdev/base` image we already pulled in task 4.

Task 6 flipped back to `[ ]` (will resume once task 5 produces an
image that actually runs envd). Task 5 flipped back to `[~]`.

Commit `16142e7` (plain `docker tag`) stays on history — the task 5
rework is a new commit on top, not a revert.

### 2026-04-15 — claim task 6 (envd-in-Docker smoke test)

Agent: Claude Opus 4.6 (1M context)

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
