# Changelog

All notable changes to edvabe land here. Format roughly follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Unreleased

### Fixed

- **Sandbox proxy no longer cuts streams that start while the request body
  is still being relayed.** Go's HTTP/1 server closed the inbound request
  body as soon as the proxied response began. When the backend answered
  before the transport's final EOF read of that body (envd's Connect
  `process.Process/Start` does this when the host is busy), the transport
  failed the request write, closed the upstream connection, and the client
  saw the stream drop (`ReverseProxy read error during body copy: … use of
  closed network connection`). The proxy now enables full duplex on every
  request, so the body keeps flowing upstream.

## v0.4.0 — 2026-06-05

### Added

- **Skip the startup envd/base rebuild via `EDVABE_REUSE_IMAGES=1`.**
  When set, `edvabe serve` reuses an existing `edvabe/base:latest` /
  `edvabe/envd-source:latest` instead of rebuilding it (the multi-minute
  envd compile). Default off — a bumped `EnvdSourceSHA` or Dockerfile
  always triggers a fresh rebuild for correctness. Intended for CI, which
  sets it after restoring the images from a cache: the cache key MUST pin
  the edvabe version so a bumped image busts it (else a stale envd would
  be reused). Opt-in.

## v0.3.0 — 2026-06-03

### Added

- **Configurable container security via `EDVABE_SECURITY_OPT`.** Sets
  `HostConfig.SecurityOpt` (comma-separated) on every spawned sandbox.
  When set it replaces edvabe's default `seccomp=unconfined,
  apparmor=unconfined`; unset keeps that default. `systempaths=unconfined`
  is recognized specially and translated to empty
  `MaskedPaths`/`ReadonlyPaths` (Docker CLI sugar the daemon rejects as an
  API-level opt) so in-sandbox bubblewrap can mount a fresh `/proc`.
  Opt-in; relaxes the sandbox's isolation.
- **Extra bind mounts via `EDVABE_EXTRA_BINDS`.** Adds bind mounts
  (`host:ctr[:ro]`, comma-separated) to every spawned sandbox on top of
  the per-request ones. Lets the operator share host fixtures (e.g. LLM
  replay snapshots) with sandboxes for deterministic local testing.
  Opt-in.

### Fixed

- Dashboard overview now lists sandboxes and templates in a stable order
  (by `CreatedAt`, then `ID`) instead of Go map iteration order.

## v0.2.0 — 2026-04-24

### Fixed

- `SetTimeout` no longer returns `ErrExpired` (410) for a paused
  sandbox whose original running-TTL has lapsed — same state-guard
  the `Connect` path got in v0.1.1. Matters when SDK code calls
  `sandbox.setTimeout()` on a paused sandbox ahead of `connect()`.

### Added

- **Sandbox registry rehydration on startup.** `edvabe serve` now
  enumerates every `edvabe.managed=true` container on the Docker
  daemon and rebuilds its in-memory sandbox registry from Docker
  labels + container state. Paused sandboxes survive edvabe
  restarts and SDK `reconnect(sandboxId)` works across them. New
  sandbox-level labels (stamped at Create time):
  - `edvabe.sandbox.template.id`
  - `edvabe.sandbox.template.alias`
  - `edvabe.sandbox.token.envd`
  - `edvabe.sandbox.token.traffic`
  - `edvabe.sandbox.ontimeout`

  Containers created before this release (missing the new labels)
  still rehydrate with `State` / `PauseMode` / resource limits /
  metadata intact, and remain destroyable. The envd access token
  is not recoverable for them, so SDK reconnect against pre-upgrade
  sandboxes will fail envd auth — destroy and recreate.

## v0.1.1 — 2026-04-24

### Fixed

- `Connect` no longer returns `ErrExpired` (410) when resuming a
  paused sandbox whose original running-TTL has lapsed. Paused
  sandboxes have their own lifecycle (`FreezeDuration` → demote,
  `StoppedGCAfter` → destroy); the running-TTL check now only applies
  to running sandboxes. Previously, any sandbox paused past its TTL
  was permanently un-resumable.

### Changed

- Sandbox containers now start with `seccomp=unconfined` and
  `apparmor=unconfined` security options. Unblocks tools that create
  user namespaces inside the sandbox — notably `bwrap`, which bun's
  install-script sandbox shells out to on Linux. Consistent with the
  single-user local-dev threat model (see `docs/05-architecture.md`).
  Existing sandboxes must be recreated to pick up the change.

## v0.1.0 — 2026-04-15

First usable release. Phase 1 goal met: a user can run `edvabe serve`
on their laptop, point the unmodified Python or TypeScript E2B SDK at
it (see README for the env vars), and exercise the full hot path
against real Docker-backed sandboxes.

### Added

- **Control plane (T0):** `GET /health`, `POST /sandboxes` (201),
  `GET /sandboxes/{id}`, `GET /v2/sandboxes` (paginated list),
  `DELETE /sandboxes/{id}`, `POST /sandboxes/{id}/timeout`,
  `POST /sandboxes/{id}/connect`.
- **Runtime:** Docker backend (`internal/runtime/docker`) with socket
  auto-discovery (Docker Desktop / Colima / OrbStack / Podman), create,
  destroy, stats, image pull/build, and container labeling for later
  reconnect flows.
- **Agent provider:** upstream envd @ `0.5.7` baked into
  `edvabe/base:latest` via an embedded multi-stage Dockerfile. No
  separate envd binary download or cache.
- **Reverse proxy:** header-first dispatch (`E2b-Sandbox-Id` /
  `E2b-Sandbox-Port`) with a `parseHost` fallback for
  `<port>-<id>.localhost` URLs; streaming `httputil.ReverseProxy` with
  `FlushInterval: -1` so PTY and watcher events are not coalesced.
- **CLI subcommands:** `serve`, `doctor` (four preflight checks with
  aligned-dot output and non-zero exit on failure), `build-image`,
  `pull-base`, `version`.
- **E2E suites:** `make test-e2e-python` and `make test-e2e-ts` spin up
  a real `edvabe serve` and run six tests each (create/kill,
  `commands.run`, `files.write/read/list`, PTY, `watchDir`) against
  unmodified SDKs pinned to `e2b==2.20.0` / `e2b@2.19.0`.

### Known limitations

- **`E2B_SANDBOX_URL` is mandatory.** The SDK's default
  `https://49983-<id>.<domain>` data-plane URL is HTTPS and edvabe does
  not terminate TLS. Set
  `E2B_SANDBOX_URL=http://localhost:3000` in the client env. See README
  and `docs/03-api-surface.md`.
- **Pause / resume / snapshots** are stubs — returning Phase-4 errors
  rather than committing containers.
- **Code interpreter overlay** (`@e2b/code-interpreter`) is Phase 2.
- **Template builds** beyond the pinned `edvabe/base:latest` are
  Phase 3.
- **Auth** is shape-only — `X-API-Key` must exist but is not validated.
  Local dev threat model only.
- **Metrics, teams, volumes, admin** endpoints are unimplemented (T4 —
  Phase 5).

### E2E coverage

Six tests per SDK cover the Phase-1 hot path: create, commands,
filesystem read/write/list, PTY stdin + data callback, and directory
watch. Not yet covered (intentional, queued for Phase 2 polish):
background commands, bulk filesystem ops, recursive watch, PTY resize
/ reconnect, sandbox list / reconnect / timeout endpoints,
user-bound HTTP port forwarding, and error-path responses (404 / 410 /
401).
