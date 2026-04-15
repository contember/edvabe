# Changelog

All notable changes to edvabe land here. Format roughly follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
