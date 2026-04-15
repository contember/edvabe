# 09 — Phase 3 kickoff checklist

Concrete, ordered task list for Phase 3 ("templates and pause"). Pick
the next unchecked task, do it, verify the acceptance check, mark it
done, move on.

Driving consumer: **webmaster**. Phase 3 ships when the unchanged
`webmaster/containers/templates/chrome/build.ts` builds against edvabe
and `Sandbox.create('webmaster-sandbox-chrome')` launches a working
Chrome sandbox (see Flow B in `docs/06-phases.md`).

Each task has:
- **Do**: what to implement
- **Where**: which files to create/edit
- **Acceptance**: a specific command or test that proves it's done
- **Depends on**: which tasks must be done first (implicit if sequential)

Prerequisites on the host: everything Phase 1 needed, plus a Docker
daemon that can build multi-stage images. Before touching any task, read
Phase 3's scope section in [`docs/06-phases.md`](06-phases.md) and the
template row in [`docs/03-api-surface.md`](03-api-surface.md).

---

## Task 1 — Template store skeleton

**Do.** Stand up the `internal/template` package with the persistent
metadata store. No HTTP, no builder — just the types and a JSON-backed
`Store` with mutex-guarded CRUD.

**Where.**
- `internal/template/template.go` — `Template`, `Build`, `BuildStatus`,
  `Step`, `BuildStatusWaiting|Building|Ready|Error` consts. Schema
  matches the Phase 3 scope: `{templateID, name, tags[], alias,
  cpuCount, memoryMB, startCmd, readyCmd, imageTag, createdAt,
  builds[]}`.
- `internal/template/store.go` — `Store` struct with a JSON-file
  backing (path configurable, default
  `~/.local/share/edvabe/templates.json`). Methods: `NewStore(path)`,
  `Load()`, `Create(meta) -> Template`, `Get(id) -> Template`,
  `ResolveAlias(alias) -> Template`, `List() -> []Template`,
  `Delete(id)`, `UpdateMeta(id, mutator)`. Writes flush synchronously
  under the store mutex; reads use RWMutex.
- `internal/template/idgen.go` — `NewTemplateID()`, `NewBuildID()` using
  the same base32 encoding style as `internal/sandbox/idgen.go`.
- `internal/template/store_test.go` — unit tests: round-trip persistence
  (create → close → reopen → list matches), alias lookup, delete,
  concurrent reads under RWMutex.

**Acceptance.**

```sh
go vet ./internal/template/...
go test ./internal/template/...
```

---

## Task 2 — Content-addressed file cache

**Do.** Build the content-addressed blob store for build contexts. SDK
tars each step's source files client-side, hashes them, and the server
stores them under the hash.

**Where.**
- `internal/template/filecache/filecache.go` — `Cache` struct rooted at
  a configurable dir (default `~/.cache/edvabe/template-files/`).
  Methods: `Has(hash) bool`, `Open(hash) (io.ReadCloser, error)`,
  `Put(hash, io.Reader) error` writing atomically via `*.part → rename`.
  Hash format is lowercase hex sha256 (SDK convention — verify in task
  3).
- `internal/template/filecache/signer.go` — `Signer` with `Sign(hash)
  string` and `Verify(hash, token) bool`, HMAC-SHA256 with a short
  expiry encoded into the token. Keyed by a process-local secret.
- `internal/template/filecache/filecache_test.go` — unit tests for
  atomic write, idempotent put, concurrent put race, signer round-trip
  including expiry.

**Acceptance.**

```sh
go test ./internal/template/filecache/...
```

**Depends on.** None (independent of task 1).

---

## Task 3 — Step → Dockerfile translator

**Do.** Pure function that consumes an ordered `[]Step` plus the
template's `fromImage`/`fromTemplate`/`startCmd`/`readyCmd` and emits
a `Dockerfile` string. Also copies input files for each step's
`filesHash` into a staging dir so the generated `COPY` lines resolve.

**Where.**
- `internal/template/builder/translate.go` — `Translate(input)` where
  `input` carries the base image (or parent template ID → resolved
  tag), the step list, and the build staging dir where file contexts
  live. Returns `(dockerfile string, err error)`.
- `internal/template/builder/staging.go` — `PrepareContext(cacheDir,
  buildDir, filesHashes []string) error` that extracts each required
  tar from the file cache into `buildDir/ctx/<hash>/` so the
  Dockerfile's `COPY ctx/<hash>/<src> <dest>` lines work.
- `internal/template/builder/envd_layer.go` — emits the envd injection
  suffix (`COPY --from=edvabe/envd-source …`) and the `CMD
  ["/usr/local/bin/edvabe-init"]` rewrite. `edvabe-init` source lives
  in `assets/edvabe-init.sh` via `//go:embed`.
- `internal/template/builder/translate_test.go` — table-driven tests
  covering each of the 13 step types, `fromImage` vs `fromTemplate`,
  `skipCache` cache-bust, multi-step sequences, and the envd-layer
  append. Focus on *exact string output* — the Dockerfile is the
  contract.

**Acceptance.**

```sh
go test ./internal/template/builder/...
```

**Depends on.** Task 2 (file cache is consumed by staging).

---

## Task 4 — Template CRUD HTTP endpoints

**Do.** Wire `POST /v3/templates`, `GET /templates`, `GET
/templates/{id}`, `GET /templates/aliases/{alias}`, `DELETE
/templates/{id}`, `PATCH /v2/templates/{id}` into the control router.
No builder yet — `POST /v3/templates` creates the metadata record and
returns `{templateID, buildID, names, tags, aliases, public}`; the
actual build lives in task 7.

**Where.**
- `internal/api/control/templates.go` — handler set taking a
  `templateStore` interface (small, test-friendly).
- `internal/api/control/templates_test.go` — handler-level tests with
  an in-memory fake store.
- `internal/api/control/router.go` — extend the switch to route
  `/templates`, `/v3/templates`, `/v2/templates/...`.

**Acceptance.**

```sh
go test ./internal/api/control/...
```

And a manual probe against `edvabe serve`:

```sh
curl -s -X POST http://localhost:3000/v3/templates \
  -H 'X-API-Key: dev' \
  -H 'Content-Type: application/json' \
  -d '{"name":"probe","memoryMB":1024}' | jq .
# expect {templateID, buildID, names:["probe"], tags:[], aliases:["probe"], public:false}
```

**Depends on.** Task 1.

---

## Task 5 — File cache HTTP handlers

**Do.** Expose the file cache over HTTP so the SDK's `Template.build()`
upload step can drop tars on us.

**Where.**
- `internal/api/control/files.go` — `GET
  /templates/{id}/files/{hash}` returning `{present, url?}`. On miss,
  `url` points at `POST /_upload/{hash}?token=<signed>`; on hit,
  `{present: true}` and the SDK skips upload.
- `internal/api/control/upload.go` — `POST /_upload/{hash}` that
  verifies the signer token, streams the request body through
  `filecache.Put`. 401 on invalid token, 400 on hash mismatch.
- `internal/api/control/router.go` — route both. `/_upload/...` is
  served by the same listener but deliberately outside the E2B API
  surface (no `X-API-Key` required; the HMAC token is the auth).
- `internal/api/control/files_test.go` + `upload_test.go`.

**Acceptance.**

```sh
go test ./internal/api/control/...
```

**Depends on.** Task 2.

---

## Task 6 — envd-source scratch image

**Do.** Produce a single-layer scratch image `edvabe/envd-source` that
holds the envd binary and `edvabe-init` wrapper. The template builder
references it via `COPY --from=edvabe/envd-source …`, so it must exist
locally before the first user build runs.

**Where.**
- `assets/Dockerfile.envd-source` — multi-stage build that reuses the
  envd builder stage from `assets/Dockerfile.base` (same `ENVD_SHA`
  build-arg) then copies envd + `edvabe-init.sh` into a `scratch`
  final stage.
- `assets/edvabe-init.sh` — the wrapper script from the Phase 3 scope
  (`envd --isnotfc &` + optional `$EDVABE_START_CMD` + `wait`).
- `internal/agent/upstream/envd_source.go` — `EnsureEnvdSource(ctx,
  rt)` mirrors `EnsureBaseImage`; same caching by image-reference
  check.
- `cmd/edvabe/main.go` — `serve` calls `EnsureEnvdSource` alongside
  `EnsureBaseImage` at startup. `build-image` gets a new
  `--envd-source` flag (or just builds both images unconditionally).

**Acceptance.**

```sh
./bin/edvabe build-image
docker image inspect edvabe/envd-source:latest | jq '.[0].RepoTags'
# both edvabe/base:latest and edvabe/envd-source:latest present

# The final stage is `FROM scratch`, so there is no /bin/sh to exec
# into and no default CMD. Verify the files exist by copying them out
# of a never-started container — `docker create` needs a command
# argument but we do not actually run it.
cid=$(docker create edvabe/envd-source:latest /usr/local/bin/envd)
docker cp "$cid":/usr/local/bin/envd         /tmp/envd.bin
docker cp "$cid":/usr/local/bin/edvabe-init  /tmp/edvabe-init.sh
docker rm "$cid"
test -s /tmp/envd.bin && test -s /tmp/edvabe-init.sh
```

**Depends on.** Task 3 (translator references the image tag; agree on
the tag name first).

---

## Task 7 — BuildManager state machine

**Do.** Stand up the async builder runtime. One goroutine per active
build, state machine `waiting → building → ready|error`, bounded
log ring buffer.

**Where.**
- `internal/template/builder/manager.go` — `Manager` with
  `map[buildID]*build` under a mutex. `Enqueue(templateID, buildID,
  spec) error`, `Status(buildID) (BuildStatus, string, error)`,
  `Logs(buildID, cursor, limit, direction) ([]LogEntry, nextCursor,
  error)`. Injectable `Executor` interface so tests don't need
  Docker — production wires in a real implementation backed by
  `runtime.Runtime.BuildImage`.
- `internal/template/builder/ringbuf.go` — bounded ring buffer (default
  5000 entries). Each entry is `{timestamp, level, source, msg}`.
- `internal/template/builder/manager_test.go` — drive the manager with
  a fake executor that emits canned log lines and completes/fails on
  cue. Assert state transitions, log pagination, cursor semantics, and
  concurrent-safety with `-race`.

**Acceptance.**

```sh
go test -race ./internal/template/builder/...
```

**Depends on.** Task 3.

---

## Task 8 — Build start + status + logs endpoints

**Do.** Wire the three build-lifecycle endpoints into the control
router and connect them to the BuildManager.

**Where.**
- `internal/api/control/builds.go` — handlers:
  - `POST /v2/templates/{id}/builds/{bid}` — parse
    `TemplateBuildStartV2`, validate against the template, call
    `BuildManager.Enqueue`, return 202.
  - `GET /templates/{id}/builds/{bid}/status` — returns `{status,
    reason?}`.
  - `GET /templates/{id}/builds/{bid}/logs?cursor=&limit=&direction=&level=&source=`
    — honor `cursor`, `limit`, `direction`; accept `level` and
    `source` silently.
- `internal/api/control/builds_test.go` — handler tests against a
  fake BuildManager.
- `internal/api/control/router.go` — route the three new paths.

**Acceptance.**

```sh
go test ./internal/api/control/...
```

**Depends on.** Task 7.

---

## Task 9 — Real build executor

**Do.** Bind the BuildManager's `Executor` interface to a real
implementation that orchestrates file-context extraction →
translator → `docker build` via `runtime.Runtime.BuildImage` with
streamed build logs into the ring buffer.

**Where.**
- `internal/template/builder/executor.go` — `DockerExecutor` struct,
  `Run(ctx, spec, logSink)` method. Builds into
  `~/.cache/edvabe/builds/<buildID>/`, runs
  `PrepareContext → Translate → runtime.BuildImage`, streams
  stdout/stderr lines through `logSink`.
- Extend `runtime.BuildRequest` / `docker.BuildImage` if needed so the
  builder can stream incremental progress (the current implementation
  in `internal/runtime/docker/build.go` drops the stream on the
  floor). Add a `LogWriter io.Writer` field and thread it through.
- `internal/template/builder/executor_test.go` — behind
  `-tags=integration`, same pattern as
  `internal/runtime/docker/runtime_integration_test.go`. Builds a
  trivial template (single `fromImage` + `runCmd("echo ok")`), asserts
  `ready` state and a recognizable log line.

**Acceptance.**

```sh
go test ./internal/template/... ./internal/runtime/docker/...
go test -tags=integration ./internal/template/builder/...   # requires docker
```

**Depends on.** Tasks 3, 6, 7.

---

## Task 10 — Sandbox create integration

**Do.** Teach `POST /sandboxes` to resolve `templateID` through the
template store and thread `EDVABE_START_CMD` / `EDVABE_READY_CMD` into
the container's env. Keep the Phase 1 fallback (empty or `"base"` →
`edvabe/base:latest`).

**Where.**
- `internal/sandbox/manager.go` — rework `resolveImage` to take a
  `TemplateResolver` injected via `Options`. Resolver returns
  `(imageTag, startCmd, readyCmd, err)`. On not-found, fall back to
  the base image (Phase 1 compatibility).
- `internal/template/resolver.go` — adapter around `Store` that
  satisfies the new interface.
- `internal/runtime/runtime.go` — extend `CreateRequest` with
  `StartCmd string` and `ReadyCmd string` fields; docker runtime
  injects them as env vars on `docker run`.
- `internal/runtime/docker/create.go` — set
  `EDVABE_START_CMD=<startCmd>` / `EDVABE_READY_CMD=<readyCmd>` on the
  container env.
- `cmd/edvabe/main.go` — wire the resolver into `NewManager` on
  startup; the store is shared between control plane and manager.
- Tests updated against the noop runtime and a fake resolver.

**Acceptance.**

```sh
go test ./internal/sandbox/... ./internal/api/control/... ./internal/runtime/...
```

**Depends on.** Task 1.

---

## Task 11 — readyCmd probe loop

**Do.** After `InitAgent` succeeds, if the resolved template has a
non-empty `readyCmd`, run it through envd's process RPC in a poll
loop until it returns exit 0 or the sandbox timeout budget is
exhausted. On failure: destroy the container, return 504.

**Where.**
- `internal/agent/agent.go` — extend `AgentProvider` with
  `WaitReady(ctx, endpoint, cmd) error` (or add a sibling
  `ProbeCommand` method). Default no-op when `cmd == ""`.
- `internal/agent/upstream/upstream.go` — implement the probe via the
  process RPC on envd. Use 500ms backoff with jitter; cap attempts by
  the remaining context deadline.
- `internal/sandbox/manager.go` — call it between `InitAgent` and
  registering the sandbox. No-op when the template has no `readyCmd`
  (Phase 1 fast path).
- Tests against a fake agent provider.

**Acceptance.**

```sh
go test ./internal/sandbox/... ./internal/agent/...
```

**Depends on.** Task 10.

---

## Task 12 — Pause / snapshot / resume endpoints

**Do.** Thin wire layer over the runtime's pause/commit primitives.

**Where.**
- `internal/runtime/docker/` — implement `Pause`, `Unpause`, `Commit`
  (currently stubbed). `Pause` → `ContainerPause`; `Unpause` →
  `ContainerUnpause`; `Commit` → `ContainerCommit` to the target tag.
- `internal/sandbox/manager.go` — `Pause(id)`, `Snapshot(id, name) ->
  SnapshotInfo`. Pause flips `State` to `StatePaused`; `Connect` now
  handles the paused → running transition by unpausing.
- `internal/api/control/sandboxes.go` — handlers:
  - `POST /sandboxes/{id}/pause` → 204
  - `POST /sandboxes/{id}/snapshots` → 201 with `SnapshotInfo` body
  - `POST /sandboxes/{id}/resume` — deprecated alias for `/connect`
- `internal/api/control/router.go` — route the new paths.
- Tests against the noop runtime (extend it with pause state tracking).

**Acceptance.**

```sh
go test ./internal/runtime/... ./internal/sandbox/... ./internal/api/control/...
go test -tags=integration ./internal/runtime/docker/...
```

**Depends on.** None within Phase 3 (parallel with the template flow).

---

## Task 13 — autoPause lifecycle on timeout

**Do.** When `NewSandbox.autoPause` or `lifecycle.onTimeout == "pause"`
is set, the sandbox manager pauses the container on TTL expiry
instead of destroying it. `/connect` resumes via unpause.

**Where.**
- `internal/sandbox/manager.go` — per-sandbox `onTimeout` mode
  (`"kill"` default, `"pause"`). `EnforceTimeouts` dispatches to
  `Pause` or `Destroy` accordingly.
- `internal/api/control/sandboxes.go` — parse the new request fields,
  reflect `onTimeout` back in the sandbox detail response lifecycle.
- Tests for both branches with the noop runtime's fake clock.

**Acceptance.**

```sh
go test ./internal/sandbox/... ./internal/api/control/...
```

**Depends on.** Task 12.

---

## Task 14 — TypeScript template-build E2E

**Do.** Port the Phase 3 Flow A acceptance criterion into
`test/e2e/ts/` alongside the existing `test_basic.ts`. Must run
unchanged against a working edvabe serve plus Docker daemon.

**Where.**
- `test/e2e/ts/test_template_build.ts` — uses `Template` builder from
  `e2b` SDK, builds a smoke template from `oven/bun:slim` with a
  `runCmd` that writes a file, then creates a sandbox from it and
  reads the file back. Follows the Flow A snippet in Phase 3 scope.
- `Makefile` — extend `test-e2e-ts` (or add
  `test-e2e-ts-templates`).

**Acceptance.**

```sh
make test-e2e-ts
```

Running the template test end-to-end (build + create + read + pause
+ connect) passes.

**Depends on.** Tasks 4, 5, 6, 7, 8, 9, 10, 11, 12.

---

## Task 15 — Webmaster chrome template acceptance

**Do.** Build the unmodified
`webmaster/containers/templates/chrome/build.ts` against edvabe and
boot a sandbox from it. This is the Phase 3 ship gate.

**Where.**
- `test/e2e/ts/test_webmaster_chrome.ts` (or a documented manual
  recipe under `test/e2e/ts/README.md`) — imports the real
  `template.ts` from a checked-out webmaster tree (path configurable
  via env var, skipped if unset) and runs `Template.build` against
  edvabe.
- `docs/09-phase3-checklist.md` — this file gets a "done" tick and a
  session log entry on success.

**Acceptance.**

The acceptance bar from `docs/06-phases.md`:

```sh
E2B_API_URL=http://localhost:3000 \
E2B_DOMAIN=localhost:3000 \
E2B_API_KEY=dev \
E2B_SANDBOX_URL=http://localhost:3000 \
WEBMASTER_REPO=/path/to/webmaster \
  npm test -- test_webmaster_chrome.ts
```

Ends with a green Chrome sandbox that can execute a command via
`commands.run`.

**Depends on.** Task 14.

---

## Notes for future agents

- Phase 3 is the first phase that touches the runtime interface from
  outside Phase 1 ground. If `runtime.CreateRequest` / `BuildRequest`
  needs new fields, update them in task 9 or 10 — not piecemeal — so
  the noop runtime and docker runtime stay in sync in one commit.
- Multi-arch builds are out of scope. Templates build for the host
  architecture only. Don't add `--platform` plumbing.
- `cpuCount` / `memoryMB` are stored on the template record but the
  runtime does **not** enforce them. Don't add resource limits — it's
  a local-dev tool.
- `fromImageRegistry` credentials passthrough lands with task 9. For
  the webmaster acceptance flow, `oven/bun:slim` is public, so it's
  not a blocker.
- The `edvabe doctor` output should grow an
  `edvabe/envd-source:latest` check in task 6 alongside the existing
  base-image check.
