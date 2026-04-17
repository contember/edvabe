# edvabe — project brief

**edvabe** is a single Go binary that exposes a wire-compatible subset of
the [E2B](https://e2b.dev) cloud sandbox API on a developer's laptop.
Point an unmodified E2B SDK at it via env vars and sandboxes run in local
Docker containers instead of E2B's cloud.

This file is the identity of the project — what it is, what it promises,
the architectural invariants, and where to find things. It does not
change as work progresses.

## Where to start (for any agent entering cold)

1. **This file** — project identity, promises, golden rules, conventions.
2. **[docs/](docs/)** — architecture, API surface, feature scope,
   reference material. Read the specific file your task needs.

Do NOT pre-read the whole `docs/` tree — it wastes context.

## What edvabe promises

- **Drop-in compatibility** — unmodified E2B SDK apps work after setting
  `E2B_API_URL`, `E2B_DOMAIN`, and `E2B_API_KEY` to point at local edvabe.
- **Single binary UX** — `go install ./cmd/edvabe && edvabe serve`. No
  Redis, no Postgres, no Nomad, no Firecracker.
- **Cross-platform** — Linux x86_64/arm64, macOS x86_64/arm64 (Apple
  Silicon). Windows via WSL2 is nice-to-have.
- **Local-dev threat model** — runs the user's own code on the user's
  own laptop. Not adversarial multi-tenant.

## Golden rules — do NOT break these

Load-bearing architectural decisions. Stop and ask the user if you think
you need to violate one.

1. **edvabe MUST NOT implement envd's Connect-RPC protocol itself.** The
   data plane (filesystem, process, PTY, watchers, CI overlay) is handled
   by upstream `envd` inside the container and reverse-proxied by edvabe.
   edvabe is a dumb `httputil.ReverseProxy` past the sandbox ID header.
   *Why: ships Phase 1 in ~2500 LOC instead of ~6000, and gives byte-exact
   wire compatibility with the SDK for free.*

2. **The Runtime and Agent are separate pluggable interfaces.**
   `Runtime` = where the sandbox runs (Docker in v1).
   `AgentProvider` = what speaks envd protocol inside the sandbox
   (upstream envd binary in v1).
   Both live behind interfaces in `internal/runtime/` and
   `internal/agent/`. Do not couple them.

3. **`envdVersion` is pinned to `"0.5.7"`** in every `Sandbox` response.
   This value unlocks the newest code path in every SDK branch. Do not
   change it without auditing `js-sdk/src/envd/versions.ts`.

4. **Route by `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers first**, fall
   back to parsing `Host: <port>-<id>.<rest>`. Never require wildcard DNS.

5. **No real auth.** `X-API-Key` is required-but-not-validated on control
   plane; `X-Access-Token` is forwarded to envd unchanged. edvabe is a
   single-user local dev tool. Do not add JWT/OAuth/Supabase.

6. **Templates are Docker images.** Full stop. No rootfs conversion, no
   ext4 builds, no Firecracker snapshot format.

7. **Pause/resume is `docker pause` → `docker unpause`, with an age/cap
   demote to `docker stop` → `docker start`.** Freshly-paused sandboxes
   hold RAM (instant resume); after `EDVABE_PAUSE_FREEZE_DURATION` or
   past the `EDVABE_MAX_FROZEN_SANDBOXES` cap (LRU on `PausedAt`) they
   are demoted via `docker stop` (in-memory state lost, resume requires
   a fresh agent InitAgent). Stopped sandboxes are GC'd after
   `EDVABE_PAUSE_GC_AFTER`. No `docker commit`, no memory snapshot —
   document the caveat in user-facing messages.

8. **Atomic git commits with explicit file lists** — `git add path/to/a
   path/to/b && git commit -m "..."` in one bash command. Never
   `git add -A` or `git add .`. See `~/CLAUDE.md` for the global rule.

## Commands

```sh
# build + run
make build                      # go build -o bin/edvabe ./cmd/edvabe
make run                        # ./bin/edvabe serve
go run ./cmd/edvabe serve       # run without building
go run ./cmd/edvabe doctor      # preflight check

# test
make test                       # go test ./...
go test ./internal/runtime/...  # scoped test run
go test -run TestCreate ./...   # single test

# lint
make lint                       # golangci-lint run (if installed) + go vet
go vet ./...                    # built-in vet
```

Commands not implemented yet (Phase 1 in progress — see
[status.md](status.md)):

```sh
go run ./cmd/edvabe pull-base   # pull upstream e2bdev/base    — task 4
go run ./cmd/edvabe build-image # tag as edvabe/base:latest    — task 5
```

## Upstream E2B sources (ground truth)

When in doubt about wire shapes, the upstream repos are authoritative:

- **OpenAPI spec** (control plane) — `https://github.com/e2b-dev/infra/blob/main/spec/openapi.yml`
- **envd proto files** (filesystem + process RPCs) — `https://github.com/e2b-dev/e2b/tree/main/spec/envd`
- **envd source** (Go) — `https://github.com/e2b-dev/infra/tree/main/packages/envd`
- **JS SDK** (wire inspector) — `https://github.com/e2b-dev/e2b/tree/main/packages/js-sdk/src`
- **Python SDK** (wire inspector) — `https://github.com/e2b-dev/e2b/tree/main/packages/python-sdk/e2b`
- **Code interpreter template** — `https://github.com/e2b-dev/code-interpreter/tree/main/template`

Key files cited throughout `docs/`:
- `infra/packages/envd/main.go:64-69` — `--isnotfc` dev flag we depend on
- `infra/packages/envd/internal/api/init.go` — `/init` handshake shape
- `infra/packages/shared/pkg/proxy/host.go:41-66` — sandbox-URL parser
- `e2b/packages/js-sdk/src/envd/versions.ts` — envdVersion branch points
- `e2b/packages/js-sdk/src/sandbox/index.ts:149-151` — where the SDK sends
  `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers

## Code conventions

Short version. No formal style guide.

- **Go 1.22+**, standard `gofmt`, `goimports`.
- **Structured logging via `log/slog`** with a request ID per request.
- **Errors**: wrap with `fmt.Errorf("doing X: %w", err)`; return early;
  don't `log.Fatal` outside `main.go`.
- **Error responses** to clients use the E2B envelope
  `{"code": <int>, "message": <str>}` via a shared helper in
  `internal/api/errors.go`.
- **Tests**: `*_test.go` next to the code. Integration/conformance tests
  in `internal/conformance/`. E2E tests in `test/e2e/python/` and
  `test/e2e/ts/`.
- **No doc comments on trivial helpers.** Add a short one-line comment
  only when the WHY is non-obvious (see `~/CLAUDE.md` global rule).
- **Imports** grouped: stdlib, third-party, internal.

## Things we are NOT doing

Before writing code that touches any of these, check
[docs/01-overview.md](docs/01-overview.md) non-goals and
[docs/06-phases.md](docs/06-phases.md):

- Firecracker, libkrun, KVM, microVMs (deferred to Phase 6+)
- A native Go envd reimplementation (deferred to Phase 5, optional)
- Real auth (JWT/OAuth/Supabase)
- Multi-tenant, billing, quotas, teams
- Nomad, Consul, Redis, Postgres, Clickhouse
- nftables egress filtering
- Wildcard DNS setup
