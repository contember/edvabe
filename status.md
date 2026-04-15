# edvabe project status

Living document. Agents update this when picking up, completing, or
blocking on tasks. See [instructions.md](instructions.md) for the
update protocol.

## Current phase

**Phase 3 — "Templates and pause"**
Full task definitions: [docs/09-phase3-checklist.md](docs/09-phase3-checklist.md)

Phase ordering rationale: webmaster is the driving consumer and needs
programmatic `Template.build()` + pause/resume but does not use the
code interpreter overlay — see `docs/06-phases.md:11-22`. Phase 2
(code interpreter) is deferred until a consumer actually needs it.

## Phase 3 tasks

Legend: `[ ]` not started · `[~]` in progress · `[x]` done

- [~] **Task 1 — Template store skeleton**
- [ ] **Task 2 — Content-addressed file cache**
- [ ] **Task 3 — Step → Dockerfile translator**
- [ ] **Task 4 — Template CRUD HTTP endpoints**
- [ ] **Task 5 — File cache HTTP handlers**
- [ ] **Task 6 — envd-source scratch image**
- [ ] **Task 7 — BuildManager state machine**
- [ ] **Task 8 — Build start + status + logs endpoints**
- [ ] **Task 9 — Real build executor**
- [ ] **Task 10 — Sandbox create integration**
- [ ] **Task 11 — readyCmd probe loop**
- [ ] **Task 12 — Pause / snapshot / resume endpoints**
- [ ] **Task 13 — autoPause lifecycle on timeout**
- [ ] **Task 14 — TypeScript template-build E2E**
- [ ] **Task 15 — Webmaster chrome template acceptance**

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
- [x] **Task 9 — Dispatch + reverse proxy** (971265a, 2026-04-15)
      `internal/api/` package: `parseHost` (modeled on upstream
      `shared/pkg/proxy/host.go`), `NewRouter` for header/host
      dispatch, `NewProxy` with `FlushInterval: -1` streaming
      `httputil.ReverseProxy`. `SandboxLookup` and `AgentResolver`
      declared as small local interfaces so tests inject fakes;
      production wires through `*sandbox.Manager` and
      `runtime.Runtime`. 9 unit tests + 11 parseHost subcases,
      includes a timing-based assertion that chunks flow without
      being coalesced by the proxy.
- [x] **Task 10 — Control plane: health + create + get** (ac1870a, 2026-04-15)
      `internal/api/control/` now serves `GET /health`, `POST /sandboxes`,
      and `GET /sandboxes/{id}` behind require-only `X-API-Key` auth,
      backed by the real sandbox manager + Docker runtime. `serve` now
      boots the runtime, ensures `edvabe/base:latest`, starts the watchdog,
      and exposes the first working end-to-end control plane. Added the
      upstream envd provider (`Ping` + `/init`) and consolidated the shared
      `{code,message}` error envelope.
- [x] **Task 11 — Control plane: list + delete + timeout + connect** (2ab1d1f, 2026-04-15)
      `internal/api/control/sandboxes.go` now adds `GET /v2/sandboxes`,
      `DELETE /sandboxes/{id}`, `POST /sandboxes/{id}/timeout`, and
      `POST /sandboxes/{id}/connect` on top of task 10. List supports
      `state`, `limit`, and `nextToken`; timeout/connect translate
      manager `ErrNotFound`/`ErrExpired` into 404/410 envelopes.
- [x] **Task 12 — Python SDK E2E test** (56095ec, 2026-04-15)
      `test/e2e/python/{conftest.py,test_basic.py,requirements.txt}` + `Makefile`
      target `test-e2e-python` boot edvabe, wait for `/health`, run pytest in a
      local venv, tear down. Six tests cover create/kill, commands.run,
      files.write/read/list, PTY (via `sbx.pty`), and watch_dir. Fixed two
      real bugs surfaced by the SDK: (1) `POST /sandboxes` now returns 201
      (was 200 — SDK parser silently returned `parsed=None` on 200); (2)
      documented that clients must set `E2B_SANDBOX_URL=http://localhost:3000`
      so the SDK skips `https://49983-<id>.<domain>` host synthesis and
      routes through edvabe using the `E2b-Sandbox-Id`/`Port` headers.
- [x] **Task 13 — TypeScript SDK E2E test** (fedfdd4, 2026-04-15)
      `test/e2e/ts/{package.json,tsconfig.json,test_basic.ts}` + `Makefile`
      target `test-e2e-ts`. Uses `e2b@2.19.0` + `tsx --test` on node 24's
      built-in test runner; six tests mirror the Python suite exactly
      (create/kill, commands.run, files.write/read/list, pty, watchDir).
      Ran clean on the first attempt — task-12 server fixes (201 on create,
      `E2B_SANDBOX_URL` routing) carry over, so no new edvabe bugs surfaced.
- [x] **Task 14 — Doctor subcommand** (eef7eed, 2026-04-15)
      `internal/doctor/doctor.go` runs four preflight checks and prints an
      aligned pass/fail table: Docker socket, Docker version ≥ 20.10,
      `edvabe/base:latest` present, bind port free. `cmd/edvabe/main.go`
      wires `doctor` with `--port` / `--image` flags. Non-zero exit on
      any failure; downstream checks short-circuit with `skipped: no
      daemon connection` when the socket check fails so a user gets the
      full picture in one run. The original checklist had a stale envd
      binary cache check — dropped in a separate commit (f63b980) per
      the instructions.md rule for fixing task descriptions.
- [~] **Task 15 — Tag v0.1.0** (local tag created on 5ded213;
      awaiting user confirmation before `git push origin v0.1.0`)

## Phase 2+ (not yet active)

See [docs/06-phases.md](docs/06-phases.md) for Phases 2–5 scope. No task
breakdown yet — create a `docs/09-phase2-checklist.md` (or similar) when
Phase 1 is complete.

## Session log

Newest first. Keep entries tight. Reference commit hashes so future
agents can `git show` the actual changes.

### 2026-04-15 — phase 3 kickoff + claim task 1 (template store)

Agent: Claude Opus 4.6 (1M context)

- Scoped Phase 3 in detail after the user pointed at the new
  programmatic E2B `Template()` SDK in use by webmaster
  (`webmaster/containers/templates/chrome/template.ts` +
  `build.ts`). Updated `docs/06-phases.md` Phase 3 section with
  envd injection into user images, `edvabe-init` wrapper,
  `readyCmd` lifecycle, content-addressed file cache, template
  metadata persistence, alias resolution in sandbox create,
  async builder state machine, `fromTemplate` chaining,
  `fromImageRegistry` passthrough. Resource limits (`cpuCount` /
  `memoryMB`) intentionally stored-but-not-enforced per user
  guidance; multi-arch explicitly out of scope.
- `docs/03-api-surface.md` — T3 header corrected to Phase 3
  (was Phase 4), endpoint table gained
  `GET /templates/{id}/files/{hash}` + internal
  `POST /_upload/{hash}`, body shapes expanded.
- Created `docs/09-phase3-checklist.md` with 15 tasks broken out
  and ordered by dependency (store → file cache → translator →
  CRUD → file-cache HTTP → envd-source image → builder manager →
  build endpoints → real executor → sandbox create integration →
  readyCmd probe → pause/snapshot → autoPause → E2E → webmaster).
- Switched `status.md` current phase to Phase 3, listed the task
  board, claimed task 1.
- Phase 2 (code interpreter) deferred until a consumer needs it.
- v0.1.0 tag from the previous session is still local-only and
  still waiting on explicit push approval.

### 2026-04-15 — claim task 15 (tag v0.1.0)

Agent: Claude Opus 4.6 (1M context)

- Docs follow-up from tasks 12/13/14 resolved: README rewritten with
  the four env vars and a working Python snippet, `docs/03-api-surface.md`
  corrected (the old "dispatch without wildcard DNS" claim ignored
  that the SDK defaults to HTTPS), new top-level `CHANGELOG.md` with
  the v0.1.0 entry.
- Ran the full acceptance battery on this host:
  - `make build test lint` → clean (`go vet` clean, golangci-lint not
    installed so skipped per Makefile).
  - `make test-e2e-python` → 6/6 passed, 2.63s.
  - `make test-e2e-ts` → 6/6 passed, 2.78s.
  - `./bin/edvabe doctor` → 4/4 OK, exit 0.
- Annotated tag `v0.1.0` created **locally** on `5ded213`
  (`git tag -a v0.1.0 -m ...`). **Not pushed.** Per the execute-with-
  care rule, pushing tags is a visible-to-others, hard-to-reverse
  action and needs explicit user confirmation; the session log will
  be updated to [x] once the user greenlights the push.
- Commits: `5ded213` (release prep).
- Open follow-ups: push the tag once the user confirms, then open
  Phase 2 planning per `docs/06-phases.md`.

### 2026-04-15 — claim task 14 (doctor subcommand)

Agent: Claude Opus 4.6 (1M context)

### 2026-04-15 — complete task 14 (doctor subcommand)

Agent: Claude Opus 4.6 (1M context)

- Fixed the task-14 description first (`f63b980`): the original listed
  "envd binary cache populated" as a fifth check, but that cache no
  longer exists — task 5 replaced the envd download with an in-Docker
  multi-stage build inside `edvabe/base`. Dropped the bullet and added a
  one-paragraph note explaining why.
- Implemented `internal/doctor/doctor.go` with four checks run
  sequentially:
  1. **Docker socket** — `docker.DiscoverHost` + `client.Ping` with a 3s
     timeout.
  2. **Docker version** — `ServerVersion` + `parseMajorMinor`
     (hand-rolled to tolerate suffixes like `+dfsg1`); compares against
     `minDockerMajor=20`, `minDockerMinor=10`.
  3. **edvabe/base:latest image** — `ImageList` with a `reference=<tag>`
     filter; on missing, suggests `edvabe build-image`.
  4. **Port 3000 free** — `net.Listen(":N")` then immediate close; clean
     and fast.
- Downstream checks that depend on an already-established connection
  reuse a `runState{host, cli}` and short-circuit to
  `FAIL (skipped: no daemon connection)` when the socket check failed,
  so a user without Docker running still gets a four-line report.
- `cmd/edvabe/main.go` wires the subcommand with `--port` (defaults 3000)
  and `--image` (defaults `sandbox.DefaultImage`). `os.Exit(1)` on any
  failure; pure stdout for the table.
- Unit tests in `internal/doctor/doctor_test.go` cover
  `parseMajorMinor`, the all-pass and mixed-fail `printResults` layouts,
  and the happy-path `checkPortFree(0)` (kernel-picked port).
- Manually exercised:
  - Happy path — all 4 OK, exit 0.
  - `--image=nonexistent/tag:latest --port=22` — 2 FAIL (missing image,
    bind perm denied), exit 1.
  - `DOCKER_HOST=unix:///tmp/definitely-not-docker.sock` — Docker socket
    FAIL, two downstream skipped, port OK, exit 1.
- Files:
  - `docs/08-phase1-checklist.md` (task-description fix, separate commit)
  - `internal/doctor/doctor.go` (new)
  - `internal/doctor/doctor_test.go` (new)
  - `cmd/edvabe/main.go` (stub → real wiring)
- Acceptance:
  - `go test ./...` passes.
  - `./bin/edvabe doctor` on this host prints 4 × OK with exit 0.
- Commits: `f63b980` (checklist fix), `eef7eed` (implementation).
- Open follow-ups: none new. Task 15 (tag v0.1.0) is next; still needs
  the `E2B_SANDBOX_URL` docs update flagged in tasks 12/13.

### 2026-04-15 — claim task 13 (TypeScript SDK E2E test)

Agent: Claude Opus 4.6 (1M context)

### 2026-04-15 — complete task 13 (TypeScript SDK E2E test)

Agent: Claude Opus 4.6 (1M context)

- Added `test/e2e/ts/` mirroring `test/e2e/python/`: `package.json` with
  `e2b@2.19.0` + `tsx@4.19.2`, `tsconfig.json`, `test_basic.ts`. Runs via
  node 24's built-in test runner (`tsx --test test_basic.ts`) so no vitest
  / jest dependency.
- Six tests 1:1 with Python: create+kill, `commands.run`, `files.write/read`,
  `files.list`, `pty.create`+`sendInput`, `files.watchDir`. TS SDK exposes
  nicer callback-style APIs for `onData` / watch, so the TS suite is
  slightly more compact than the Python one.
- `Makefile` target `test-e2e-ts` mirrors `test-e2e-python` (build →
  boot serve → wait `/health` → run tests → trap teardown). First
  attempt failed with `/bin/sh: Syntax error: "(" unexpected` because
  dash doesn't allow env-var prefix in front of a `( subshell )`. Fixed
  by using `cd $(E2E_TS_DIR) && ENV=... npm test` instead.
- No new edvabe bugs surfaced. Task-12's `201 Created` + `E2B_SANDBOX_URL`
  story carries over verbatim to the TS SDK, which reads the same env
  var (confirmed in `e2b/dist/index.d.ts` line ~2846).
- Files:
  - `test/e2e/ts/package.json`
  - `test/e2e/ts/tsconfig.json`
  - `test/e2e/ts/test_basic.ts`
  - `Makefile` (new `test-e2e-ts` target)
  - `.gitignore` (ignore `node_modules`, `package-lock.json`)
- Acceptance:
  - `make test-e2e-ts` → 6 passed (first real run, 3.2s).
  - `make test-e2e-python` still 6 passed (regression check).
  - `go test ./...` passes.
- Open follow-ups: none new. The outstanding docs update (advertise
  `E2B_SANDBOX_URL` in README / `docs/03-api-surface.md`) from task 12
  still pending — should land before task 15 (tag v0.1.0).

### 2026-04-15 — claim task 12 (Python SDK E2E test)

Agent: Claude Opus 4.6 (1M context)

### 2026-04-15 — complete task 12 (Python SDK E2E test)

Agent: Claude Opus 4.6 (1M context)

- Added first real SDK E2E under `test/e2e/python/` with a `Makefile`
  target that builds the binary, boots `edvabe serve --port 3000` in the
  background, polls `/health` until ready, runs pytest in a local venv,
  and reliably tears down the serve process via a bash `trap`.
- Test suite: six tests mapping to the Phase-1 acceptance hot path.
  `test_create_and_kill`, `test_commands_run_echo`, `test_files_write_read`,
  `test_files_list`, `test_pty` (via `sbx.pty.create` + background `wait`
  thread feeding an `on_pty` callback — the 06-phases snippet's
  `commands.run(pty=True)` form does not exist in the current SDK),
  `test_watch_dir` (via `WatchHandle.get_new_events` polling — snippet's
  context-manager iterator form likewise does not exist). Left the
  snippet divergences in the test file header comment.
- **Bug 1 — create returns 200 instead of 201.** The generated SDK
  client in `post_sandboxes.py` only populates `parsed` on 201, not 200,
  so every `Sandbox.create` blew up with `Body of the request is None`.
  Flipped `createSandbox` to write `http.StatusCreated` explicitly and
  updated the control-plane unit tests.
- **Bug 2 — SDK data plane defaults to HTTPS host form.** Without any
  extra env vars, the SDK builds `https://49983-<sandbox_id>.<E2B_DOMAIN>`
  which (a) uses HTTPS and (b) resolves to port 3000 via `*.localhost`
  but TLS is not served. First run failed with
  `SSL: RECORD_LAYER_FAILURE`. Fix is client-side: set
  `E2B_SANDBOX_URL=http://localhost:3000`, which short-circuits
  `get_sandbox_url` while still sending `E2b-Sandbox-Id` /
  `E2b-Sandbox-Port` headers that edvabe's router dispatches on.
  The Makefile target sets this, and the test header documents it.
  This is an onboarding env var users must set — docs update is out of
  scope for task 12 but should land before v0.1.0.
- Files:
  - `test/e2e/python/requirements.txt` (e2b==2.20.0, pytest==8.3.4)
  - `test/e2e/python/conftest.py`
  - `test/e2e/python/test_basic.py`
  - `Makefile` (new `test-e2e-python` target)
  - `internal/api/control/sandboxes.go` (201 on create)
  - `internal/api/control/router_test.go` (expect 201)
- Acceptance:
  - `make test-e2e-python` → 6 passed.
  - `go test ./...` passes.
- Commits: `56095ec` (implementation).
- Open follow-ups:
  - Update user-facing docs (README / 03-api-surface.md) to mention
    `E2B_SANDBOX_URL` before tag 0.1.0. Not part of task 12.
  - Task 13 (TypeScript E2E) will likely hit the same two bugs with the
    same fixes.

### 2026-04-15 — claim task 11 (control plane: list + delete + timeout + connect)

Agent: OpenCode (gpt-5.4)

### 2026-04-15 — complete task 11 (control plane: list + delete + timeout + connect)

Agent: OpenCode (gpt-5.4)

- Extended the task-10 control plane in `internal/api/control/` with the
  remaining Phase-1 lifecycle endpoints owned by task 11:
  `GET /v2/sandboxes`, `DELETE /sandboxes/{id}`,
  `POST /sandboxes/{id}/timeout`, and `POST /sandboxes/{id}/connect`.
- Kept the implementation minimal and manager-shaped instead of inventing a
  second persistence layer. `listSandboxes` reuses the existing in-memory
  registry snapshot, sorts it deterministically by `CreatedAt`/ID, supports
  optional `state=running,paused` filtering, and implements lightweight
  offset pagination via `limit` + `nextToken` with the response header
  `X-Next-Token`.
- `deleteSandbox` delegates to `Manager.Destroy`. `setSandboxTimeout` and
  `connectSandbox` decode `{timeout}` in seconds and map manager sentinel
  errors cleanly: `ErrNotFound` -> 404, `ErrExpired` -> 410 Gone. Running
  sandboxes reconnect with 200 and the normal sandbox payload; no paused
  resume path yet because pause is phase 4.
- Added coverage in `internal/api/control/router_test.go` for the new happy
  path flow (create x2 -> list with pagination -> connect -> timeout ->
  delete) plus missing-sandbox connect returning 404.
- Files:
  - `internal/api/control/router.go`
  - `internal/api/control/sandboxes.go`
  - `internal/api/control/router_test.go`
- Acceptance:
  - `go test ./internal/api/control/...` passes.
  - `go test ./...` passes.
  - Live task-11 curl flow passes against `go run ./cmd/edvabe serve --port 3012`:
    `GET /v2/sandboxes`, `POST /sandboxes/$SBX/timeout`,
    `POST /sandboxes/$SBX/connect`, `DELETE /sandboxes/$SBX`.
- Commits: `2ab1d1f` (implementation).
- No new open questions.

### 2026-04-15 — complete task 10 (control plane: health + create + get)

Agent: OpenCode (gpt-5.4)

- Added the first real control-plane slice under `internal/api/control/`:
  `GET /health`, `POST /sandboxes`, and `GET /sandboxes/{id}`. Routing is
  stdlib-only, scoped deliberately to task 10 — no list/delete/timeout/
  connect yet.
- Added `internal/api/auth.go` (`RequireAPIKey`) and
  `internal/api/errors.go` (`WriteError`). `GET /health` stays unauthenticated;
  sandbox routes require only a non-empty `X-API-Key` and return the E2B
  `{code,message}` envelope on failure.
- Added the real upstream agent provider in `internal/agent/upstream/`:
  `Ping` polls envd `/health`, `InitAgent` POSTs `/init`, and `EnsureImage`
  delegates to the already-pinned multi-stage base-image build. This is the
  missing runtime/manager glue that makes `sandbox.Manager.Create` work
  against actual containers instead of only test doubles.
- `cmd/edvabe/main.go` now wires `serve`: optional `--docker-socket`
  overrides `DOCKER_HOST`, Docker runtime init, `EnsureImage`, manager init,
  watchdog goroutine, control router + proxy dispatch, and `ListenAndServe`.
- Response shaping follows `docs/03-api-surface.md` but stays minimal:
  create returns `sandboxID`, `envdVersion`, `envdAccessToken`,
  `trafficAccessToken`, `domain`, `metadata`, `startedAt`, `endAt`; get adds
  `state`, placeholder lifecycle/network config, and stats-derived fields.
  One important fix during verification: reported `domain` now reflects the
  actual serve port (`localhost:<port>`), not a hard-coded `localhost:3000`.
- Files:
  - `cmd/edvabe/main.go`
  - `internal/agent/upstream/provider.go`
  - `internal/agent/upstream/provider_test.go`
  - `internal/api/auth.go`
  - `internal/api/errors.go`
  - `internal/api/proxy.go`
  - `internal/api/control/health.go`
  - `internal/api/control/router.go`
  - `internal/api/control/router_test.go`
  - `internal/api/control/sandboxes.go`
- Acceptance:
  - `go vet ./...` clean.
  - `go test ./...` passes.
  - Live task-10 curl flow passes against `go run ./cmd/edvabe serve --port 3011`:
    `/health` -> 204, `POST /sandboxes` returns a sandbox payload,
    `GET /sandboxes/{id}` returns `state: "running"`.
- Commits: `ac1870a` (implementation).
- No new open questions.

### 2026-04-15 — complete task 9 (dispatch + reverse proxy)

Agent: Claude Opus 4.6 (1M context)

- `parseHost` is a near-literal port of
  `e2b-infra/packages/shared/pkg/proxy/host.go:41-66`, fetched via
  `gh api repos/e2b-dev/infra/contents/...`. Simplified the return
  shape from upstream's `(sandboxID, port uint64, error)` to
  `(port, id string, ok bool)` because the dispatcher only wants a
  binary "proxy or not" decision — the error detail never reaches
  the client. Port stays a string so it goes straight into the
  `E2b-Sandbox-Port` header.
- `NewRouter` promotes a host-parsed subdomain into the header
  pair so downstream handlers don't need to re-parse. Explicit
  `E2b-Sandbox-Id` / `-Port` headers take precedence over the host
  when both are present (matches upstream `GetTargetFromRequest`
  which checks headers first).
- `NewProxy` declares two local interfaces (`SandboxLookup`,
  `AgentResolver`) rather than taking `*sandbox.Manager` and
  `runtime.Runtime` directly. Minor deviation from the task's
  signature sketch, but `*sandbox.Manager` and `runtime.Runtime`
  satisfy them structurally so callers don't change — and fake
  implementations in `proxy_test.go` avoid standing up a full
  Manager just to test the HTTP layer.
- Reverse proxy uses Go 1.20+'s `Rewrite` callback (not the
  deprecated `Director`) and passes the resolved target URL through
  a private context key. A single shared `ReverseProxy` is built
  per handler instead of per request so the default HTTP transport
  can reuse connections to the same agent.
- `FlushInterval: -1` is explicit even though
  `httputil.ReverseProxy` auto-detects Content-Length=-1 /
  text/event-stream as streaming. Belt and suspenders, and
  documents intent — Phase 2 code interpreter NDJSON depends on
  this being correct.
- Hop-by-hop header stripping is handled by
  `httputil.ReverseProxy`'s own transport layer, not explicitly.
  No manual `Connection`/`Upgrade`/`Transfer-Encoding` scrub needed.
  Noted in the proxy doc comment so no one adds redundant scrubbing
  later.
- Error envelope is a private `writeErrorEnvelope` helper inside
  `proxy.go` — task 10 owns the canonical
  `internal/api/errors.go` and will consolidate. Kept local to
  avoid pre-empting task 10 scope.
- `TestProxyStreamsResponse` uses a timing assertion: the backend
  sends `first\n`, sleeps 150ms, sends `second\n`. The test
  records timestamps of each line-read from the proxied response
  and asserts the first arrives well before 150ms and the gap
  between them is at least 100ms. 50ms slack keeps it stable on
  busy CI. If streaming regressed, both reads would come in at
  ~150ms after request start, failing the first-line assertion.
- Files:
  - `internal/api/parsehost.go` — `parseHost(host) (port, id, ok)`.
  - `internal/api/dispatch.go` — `NewRouter(control, proxy) http.Handler`,
    `HeaderSandboxID` / `HeaderSandboxPort` consts.
  - `internal/api/proxy.go` — `SandboxLookup`, `AgentResolver`,
    `NewProxy`, local `writeErrorEnvelope`.
  - `internal/api/parsehost_test.go` — 11 subcases.
  - `internal/api/dispatch_test.go` — 4 routing tests.
  - `internal/api/proxy_test.go` — 5 tests including streaming.
- Acceptance:
  - `go vet ./...` clean, `go build ./...` clean.
  - `go test ./internal/api/...` passes all 9 top-level tests.
  - `go test -race ./internal/api/...` clean.
  - `go test ./...` green across the full unit suite.
- Commits: `39acfe1` (claim), `971265a` (implementation).
- No new open questions.

### 2026-04-15 — claim task 9 (dispatch + reverse proxy)

Agent: Claude Opus 4.6 (1M context)

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
