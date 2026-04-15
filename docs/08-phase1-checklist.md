# 08 — Phase 1 kickoff checklist

Concrete, ordered task list for Phase 1 ("single binary runs everything").
Pick the next unchecked task, do it, verify the acceptance check, mark it
done, move on.

Each task has:
- **Do**: what to implement
- **Where**: which files to create/edit
- **Acceptance**: a specific command or test that proves it's done
- **Depends on**: which tasks must be done first (implicit if sequential)

Prerequisites on the host: Go 1.22+, Docker (or Colima/OrbStack/Podman
with Docker-compatible socket), `curl`, `make`.

Before touching any task, read [CLAUDE.md](../CLAUDE.md) at the repo root
and [docs/05-architecture.md](05-architecture.md).

---

## Task 1 — Project skeleton

**Do.** Create the Go module, CLI skeleton, Makefile, `.gitignore`, and
empty placeholder files so the build tree is walkable.

**Where.**
- `go.mod` — `go mod init github.com/<user>/edvabe` (confirm path with user first)
- `cmd/edvabe/main.go` — CLI dispatch with subcommands: `serve`, `doctor`,
  `version`, `build-image`, `fetch-envd`. All but `version` print
  "not implemented" and return 0 for now. Use `flag` or `cobra` (prefer
  stdlib `flag`).
- `Makefile` with targets: `build`, `run`, `test`, `lint`, `clean`.
- `.gitignore` — `bin/`, `*.test`, `coverage.out`, `.envrc`, `assets/envd-*`.
- `README.md` at the repo root — one-paragraph description + pointer to
  `CLAUDE.md` and `docs/`.

**Acceptance.**

```sh
make build && ./bin/edvabe version
# should print: "edvabe v0.0.0-dev (phase 1)"
./bin/edvabe serve --help
# should print usage, exit 0
```

---

## Task 2 — Runtime interface

**Do.** Define the `Runtime` interface and shared types. Noop impl for
tests.

**Where.**
- `internal/runtime/runtime.go` — interface + `CreateRequest`,
  `SandboxHandle`, `Stats`, `BuildRequest` structs.
- `internal/runtime/noop/noop.go` — in-memory noop impl used by unit
  tests of higher layers.

**Acceptance.**

```sh
go vet ./... && go build ./...
go test ./internal/runtime/...
# noop impl should pass a "can create, inspect, destroy" unit test
```

See `docs/05-architecture.md` for the exact method signatures.

---

## Task 3 — AgentProvider interface

**Do.** Define `AgentProvider` interface and `InitConfig` struct.

**Where.**
- `internal/agent/agent.go` — interface + `InitConfig`, `VolumeMount`.

**Acceptance.**

```sh
go vet ./...
```

Interface compiles. No implementation yet.

---

## Task 4 — Upstream envd: binary fetcher

**Do.** Download the upstream envd binary from GitHub releases, cache it,
verify sha256.

**Where.**
- `internal/agent/upstream/fetch.go` — `Fetch(ctx, version, arch) (path
  string, err error)`. Cache in `~/.cache/edvabe/envd/<version>/envd-linux-<arch>`.
- Pin version constant: `const DefaultEnvdVersion = "0.5.7"` (or whatever
  the latest upstream release is — verify on GitHub before pinning).
- Wire `./edvabe fetch-envd [--version <v>]` to call `Fetch` and print
  the resulting path.

**Notes.**
- Upstream release URLs look like
  `https://github.com/e2b-dev/infra/releases/download/envd-v<version>/envd` —
  **verify this path first** by checking https://github.com/e2b-dev/infra/releases.
  If the release naming is different, update the convention.
- Include sha256 verification. Either hardcode expected sum per version
  or fetch a `.sha256` sidecar alongside the binary.
- If no prebuilt release binary is published, fall back to `git clone` +
  `go build` (documented as a slow path). Flag this as a risk in
  `docs/07-open-questions.md#Q2` if encountered.

**Acceptance.**

```sh
go run ./cmd/edvabe fetch-envd
# prints: "fetched envd v0.5.7 -> /home/user/.cache/edvabe/envd/0.5.7/envd"
ls -l ~/.cache/edvabe/envd/0.5.7/envd
# file exists, executable bit set
file ~/.cache/edvabe/envd/0.5.7/envd
# reports: "ELF 64-bit LSB executable, x86-64" (or arm64)
```

---

## Task 5 — Upstream envd: base image builder

**Do.** Generate a build context containing `Dockerfile.base` +
the cached envd binary, then `docker build` it to tag
`edvabe/base:latest`.

**Where.**
- `assets/Dockerfile.base` — embedded Dockerfile. See
  `docs/05-architecture.md#phase-1-implementation-internalagentupstream`
  for the expected contents.
- `internal/agent/upstream/image.go` — `BuildBaseImage(ctx, runtime, tag
  string) error`. Uses `//go:embed` to pull in the Dockerfile, copies the
  cached envd binary next to it in a tempdir, calls
  `runtime.BuildImage()`.
- Wire `./edvabe build-image [--force]` to call `BuildBaseImage`.

**Acceptance.**

```sh
go run ./cmd/edvabe fetch-envd
go run ./cmd/edvabe build-image
docker images edvabe/base
# REPOSITORY          TAG       IMAGE ID   CREATED         SIZE
# edvabe/base         latest    ...        seconds ago     ~400MB

docker run --rm -d --name edvabe-smoke -p 49983:49983 edvabe/base:latest
curl -sf http://localhost:49983/health
# exit code 0, 204 No Content
docker rm -f edvabe-smoke
```

---

## Task 6 — envd-in-Docker smoke test (resolves Q3)

**Do.** Verify upstream envd actually functions when run outside
Firecracker. This resolves [docs/07-open-questions.md#Q3](07-open-questions.md).

**Where.**
- `test/smoke/envd_in_docker.sh` — bash script, exit 0 on pass.
- Runs the base image, curls `/health`, posts to `/init` with a dummy
  access token + env vars, makes one `process.Process/Start` Connect-RPC
  call via curl (e.g. `echo hello`), verifies the response contains
  `StartEvent` and `EndEvent`.
- Document findings in `docs/07-open-questions.md#Q3` — e.g. "works with
  no extra capabilities" or "requires `--cap-add=SYS_ADMIN`" or "MMDS
  polling logs warnings, no functional impact".

**Acceptance.**

```sh
./test/smoke/envd_in_docker.sh
# prints step-by-step output, exits 0
```

**If this task fails** — stop Phase 1, document the failure mode, and
either patch upstream envd, ship required container capabilities, or
(worst case) revisit Q1 and switch to a native Go agent plan.

---

## Task 7 — Docker runtime implementation

**Do.** Implement `Runtime` against the Docker socket.

**Where.**
- `internal/runtime/docker/runtime.go` — struct, constructor with socket
  path discovery (`/var/run/docker.sock`, `~/.colima/docker.sock`,
  `~/.orbstack/run/docker.sock`, etc.)
- `internal/runtime/docker/create.go` — `Create` → `ContainerCreate` +
  `ContainerStart` + `ContainerInspect` → return `SandboxHandle` with
  bridge IP + 49983.
- `internal/runtime/docker/destroy.go` — `Destroy` → `ContainerRemove{Force: true}`.
- `internal/runtime/docker/stats.go` — `Stats` → `ContainerStats`.
- `internal/runtime/docker/build.go` — `BuildImage` → `ImageBuild`.
- `internal/runtime/docker/endpoint.go` — `AgentEndpoint(sandboxID)` looks
  up the container's bridge IP.
- Use `github.com/docker/docker/client`. See
  [docs/07-open-questions.md#Q12](07-open-questions.md).

**Acceptance.**

```sh
go test ./internal/runtime/docker/...
# integration test:
#  - needs Docker running
#  - Create a container from alpine:latest
#  - Inspect → assert bridge IP is reachable
#  - Destroy
#  - Assert gone
```

Gate this test behind `-tags=integration` so it's skipped in unit runs.

---

## Task 8 — Sandbox manager

**Do.** In-memory sandbox registry with ID/token minting and timeout
watchdog.

**Where.**
- `internal/sandbox/sandbox.go` — `Sandbox` struct (see
  `docs/05-architecture.md#sandbox-state-and-persistence`).
- `internal/sandbox/manager.go` — `Manager` struct, `Create`, `Get`,
  `List`, `Destroy`, `Connect`, `SetTimeout`, `EnforceTimeouts`.
- `internal/sandbox/idgen.go` — `NewSandboxID()` returns `isb_<ulid>`;
  `NewEnvdToken()` returns `ea_<base64url random>`;
  `NewTrafficToken()` returns `ta_<base64url random>`.
- Ticker-based timeout enforcement, not per-sandbox goroutines.

**Acceptance.**

```sh
go test ./internal/sandbox/...
# unit tests with noop runtime:
#  - Create sandbox, Get, List returns it
#  - Destroy, List returns empty
#  - SetTimeout, advance clock, sandbox auto-killed
#  - Connect to existing, Connect to expired (expected error)
```

Use `clockwork` or a similar injectable clock for testing.

---

## Task 9 — Dispatch + reverse proxy

**Do.** Header-based routing and a passthrough reverse proxy for envd
traffic.

**Where.**
- `internal/api/dispatch.go` — `NewRouter(control, proxy http.Handler)`.
  See `docs/05-architecture.md#routing-by-header` for the exact logic.
- `internal/api/parsehost.go` — `parseHost(host string) (port, id string,
  ok bool)`. Copy from `e2b-infra/packages/shared/pkg/proxy/host.go:41-66`.
- `internal/api/proxy.go` — `NewProxy(manager *sandbox.Manager, runtime
  runtime.Runtime)` returns an `http.Handler` that looks up the sandbox by
  `E2b-Sandbox-Id` header, fetches the agent endpoint from the runtime,
  and forwards via `httputil.ReverseProxy`.

**Critical details.**
- The proxy MUST preserve streaming — set `FlushInterval: -1` on the
  `ReverseProxy` so flushes are passed through immediately. Without this,
  Connect-RPC server-streams will buffer until the response closes.
- Strip hop-by-hop headers (`Connection`, `Keep-Alive`, `TE`, `Trailers`,
  `Transfer-Encoding`, `Upgrade`).
- Do NOT read the body — just pass it through.

**Acceptance.**

```sh
go test ./internal/api/...
# unit tests:
#  - parseHost parses "49983-isb_abc.localhost" correctly
#  - dispatch routes "no header" → control, "with header" → proxy
#  - proxy streams from an httptest.Server (streaming response)
#    and the client sees chunks as they arrive, not all at the end
```

---

## Task 10 — Control-plane handlers: health + create + get

**Do.** First end-to-end slice. After this task, a curl can create and
inspect a sandbox.

**Where.**
- `internal/api/errors.go` — `WriteError(w, code, message)` writes E2B
  envelope.
- `internal/api/control/health.go` — `GET /health` → 204.
- `internal/api/control/sandboxes.go` — `POST /sandboxes`,
  `GET /sandboxes/{id}`. Decode `NewSandbox`, call `manager.Create`,
  serialize `Sandbox` or `SandboxDetail` per `docs/03-api-surface.md`.
- `internal/api/control/router.go` — chi (or stdlib mux) router mounting
  all control-plane routes.
- `cmd/edvabe/main.go` — wire the `serve` subcommand: create runtime,
  agent provider, manager, router, dispatch, HTTP server on
  `--port 3000`.

**Acceptance.** Starts edvabe and makes a manual curl request end to end.

```sh
# Terminal A
go run ./cmd/edvabe serve

# Terminal B
curl -sf http://localhost:3000/health
# 204

curl -sf -H 'X-API-Key: dev' -H 'Content-Type: application/json' \
  -d '{"templateID":"base","timeout":120}' \
  http://localhost:3000/sandboxes | jq .
# {
#   "sandboxID": "isb_...",
#   "envdVersion": "0.5.7",
#   "envdAccessToken": "ea_...",
#   ...
# }

SBX=isb_<id from above>
curl -sf -H 'X-API-Key: dev' http://localhost:3000/sandboxes/$SBX | jq .state
# "running"
```

---

## Task 11 — Control-plane handlers: list, delete, timeout, connect

**Do.** Fill in the remaining T0 control-plane endpoints.

**Where.**
- `internal/api/control/sandboxes.go` additions: `GET /v2/sandboxes` (with
  pagination header), `DELETE /sandboxes/{id}`,
  `POST /sandboxes/{id}/timeout`, `POST /sandboxes/{id}/connect`.

**Acceptance.**

```sh
curl -sf -H 'X-API-Key: dev' http://localhost:3000/v2/sandboxes | jq .
# [{...}, ...]

curl -sf -X POST -H 'X-API-Key: dev' -H 'Content-Type: application/json' \
  -d '{"timeout": 300}' \
  http://localhost:3000/sandboxes/$SBX/timeout
# 204

curl -sf -X POST -H 'X-API-Key: dev' -H 'Content-Type: application/json' \
  -d '{"timeout": 60}' \
  http://localhost:3000/sandboxes/$SBX/connect | jq .
# {"sandboxID":"...", ...}

curl -sf -X DELETE -H 'X-API-Key: dev' \
  http://localhost:3000/sandboxes/$SBX
# 204
```

---

## Task 12 — Python SDK E2E test

**Do.** First real SDK test. Unblocks the Phase 1 acceptance criterion in
[docs/06-phases.md](06-phases.md).

**Where.**
- `test/e2e/python/pyproject.toml` or `requirements.txt` with `e2b>=...`
  pinned.
- `test/e2e/python/test_basic.py` — the exact snippet from
  `docs/06-phases.md#acceptance-criterion` (Phase 1).
- `Makefile` target `test-e2e-python` that:
  1. Starts `edvabe serve` in the background.
  2. Waits for `curl localhost:3000/health` to succeed.
  3. Runs `pytest test/e2e/python/`.
  4. Kills edvabe.

**Acceptance.**

```sh
make test-e2e-python
# pytest reports all passed
```

Expect failures. Each failure is a pointer to something to fix in the
proxy, in envd's in-Docker behavior, or in a control-plane handler.
Iterate.

---

## Task 13 — TypeScript SDK E2E test

**Do.** Same as Task 12, TypeScript side.

**Where.**
- `test/e2e/ts/package.json` with `@e2b/sdk` pinned, `tsx` or similar.
- `test/e2e/ts/test_basic.ts` — mirror of Python test.
- `Makefile` target `test-e2e-ts`.

**Acceptance.**

```sh
make test-e2e-ts
# all TS tests pass
```

---

## Task 14 — Doctor subcommand

**Do.** A preflight diagnostic that tells the user why `edvabe serve`
will (or won't) work.

**Where.**
- `internal/doctor/doctor.go` — checks and a `Run(ctx) error` that prints
  a table and exits non-zero on failure.
- Checks: Docker socket reachable, Docker version ≥ 20.10,
  `edvabe/base:latest` present (suggests `edvabe build-image` if not),
  port 3000 free, envd binary cache populated.
- `cmd/edvabe/main.go` wires `doctor` subcommand.

**Acceptance.**

```sh
./bin/edvabe doctor
# Docker socket ..................... OK (/var/run/docker.sock)
# Docker version .................... OK (26.1.4)
# edvabe/base image ................. OK
# Port 3000 free .................... OK
# envd cache ........................ OK (~/.cache/edvabe/envd/0.5.7/envd)
#
# All checks passed.

./bin/edvabe doctor  # with Docker stopped
# Docker socket ..................... FAIL (connection refused)
# ...
# exit code 1
```

---

## Task 15 — Tag v0.1.0 and celebrate

**Do.** Once every task above passes its acceptance check, tag a release.

**Where.**
- `CHANGELOG.md` — short v0.1.0 entry listing what works.
- `git tag v0.1.0`.

**Acceptance.** All of:

- `make build test lint` passes on a clean checkout.
- `make test-e2e-python` passes.
- `make test-e2e-ts` passes.
- `./bin/edvabe doctor` green on a fresh laptop.
- Fresh-machine test: clone the repo, `go install ./cmd/edvabe`,
  `edvabe serve`, run the Python SDK hot-path example, everything works
  with no manual intervention beyond `edvabe doctor` and
  `edvabe build-image` prompts.

---

## After Phase 1

Open [docs/06-phases.md](06-phases.md) for Phase 2 (code interpreter) and
onwards. If Phase 1 revealed new open questions, add them to
[docs/07-open-questions.md](07-open-questions.md) before starting Phase 2.

## Status tracking

When a task is completed, add a line at the top of this file under a
`## Completed tasks` section with the task number, date, and commit hash
that landed it. Do not delete the task entries themselves — future agents
and the user benefit from the historical context.
