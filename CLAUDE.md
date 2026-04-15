# edvabe — agent brief

**edvabe** is a single Go binary that exposes a wire-compatible subset of
the [E2B](https://e2b.dev) cloud sandbox API on a developer's laptop.
Point an unmodified E2B SDK at it via env vars and sandboxes run in local
Docker containers instead of E2B's cloud.

## Read these before you write code

In order, stop when you have enough context for the task:

1. **[docs/README.md](docs/README.md)** — TL;DR and doc index.
2. **[docs/01-overview.md](docs/01-overview.md)** — goals, non-goals, injection point.
3. **[docs/05-architecture.md](docs/05-architecture.md)** — how edvabe is laid out. Read this before touching any code.
4. **[docs/08-phase1-checklist.md](docs/08-phase1-checklist.md)** — the concrete task list for Phase 1. **Pick your next task from here.**
5. **[docs/03-api-surface.md](docs/03-api-surface.md)** — request/response shapes, Connect-RPC details, tiers.
6. **[docs/02-e2b-internals.md](docs/02-e2b-internals.md)** — reference material on how real E2B is built (what we're emulating / skipping).
7. **[docs/04-runtime-decision.md](docs/04-runtime-decision.md)** — *why* Docker, not Firecracker. Only needed if someone proposes revisiting.
8. **[docs/06-phases.md](docs/06-phases.md)** — big-picture delivery phases beyond Phase 1.
9. **[docs/07-open-questions.md](docs/07-open-questions.md)** — unresolved design questions. Check before making decisions that feel like they might conflict with something.

## Current state

- **Phase 1 in progress.** See [docs/08-phase1-checklist.md](docs/08-phase1-checklist.md) for the concrete task breakdown.
- No Go code exists yet. Phase 1 Task 1 is "create the module skeleton."

## Golden rules — do NOT break these

These are load-bearing architectural decisions already made. If you think
you need to violate one, stop and ask the user first.

1. **edvabe MUST NOT implement envd's Connect-RPC protocol itself.** The
   data plane (filesystem, process, PTY, watchers, CI overlay) is handled
   by upstream `envd` inside the container and reverse-proxied by edvabe.
   edvabe is a dumb `httputil.ReverseProxy` past the sandbox ID header.
   *Why: ships Phase 1 in ~2500 LOC instead of ~6000, and gives byte-exact
   wire compatibility with the SDK for free.*

2. **The Runtime and Agent are separate pluggable interfaces** —
   `Runtime` = where the sandbox runs (Docker in v1), `Agent` = what
   speaks envd protocol inside the sandbox (upstream envd binary in v1).
   Both live behind interfaces in `internal/runtime/` and
   `internal/agent/`. Do not couple them.

3. **`envdVersion` is pinned to `"0.5.7"`** in every `Sandbox` response.
   This value unlocks the newest code path in every SDK branch we've
   audited. Do not change it without checking every SDK version-guard in
   `js-sdk/src/envd/versions.ts`.

4. **Route by `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers first**, fall
   back to parsing `Host: <port>-<id>.<rest>`. Never require wildcard DNS.

5. **No real auth.** `X-API-Key` is required-but-not-validated on control
   plane; `X-Access-Token` is forwarded to envd unchanged. edvabe is a
   single-user local dev tool. Do not add JWT/OAuth/Supabase.

6. **Templates are Docker images.** Full stop. No rootfs, no ext4 conversion,
   no Firecracker snapshot format.

7. **Pause/resume is `docker pause` / `docker commit`.** It is not a live
   memory snapshot. Document the caveat in user-facing messages.

8. **Atomic git commits with explicit file lists** — `git add path/to/a
   path/to/b && git commit -m "..."` in one bash command. Never
   `git add -A` or `git add .`. See `~/CLAUDE.md` for the global rule.

## Commands

Not all exist yet — Phase 1 creates them. Use whichever applies.

```sh
# build + run
make build                      # go build -o bin/edvabe ./cmd/edvabe
make run                        # ./bin/edvabe serve
go run ./cmd/edvabe serve       # run without building
go run ./cmd/edvabe doctor      # preflight check

# test
make test                       # go test ./...
make test-e2e                   # run E2E tests against a running edvabe
go test ./internal/runtime/...  # scoped test run
go test -run TestCreate ./...   # single test

# lint
make lint                       # golangci-lint run
go vet ./...                    # built-in vet

# images (after Phase 1 task 4+5)
go run ./cmd/edvabe fetch-envd  # fetch upstream envd binary to cache
go run ./cmd/edvabe build-image # build edvabe/base:latest
```

## Upstream E2B sources (ground truth)

When in doubt about request/response shapes, the upstream repos are the
authoritative source. Clone locally when needed:

- **OpenAPI spec** (control plane) — `https://github.com/e2b-dev/infra/blob/main/spec/openapi.yml`
- **envd proto files** (filesystem + process RPCs) — `https://github.com/e2b-dev/e2b/tree/main/spec/envd`
- **envd source** (Go) — `https://github.com/e2b-dev/infra/tree/main/packages/envd`
- **JS SDK** (wire inspector) — `https://github.com/e2b-dev/e2b/tree/main/packages/js-sdk/src`
- **Python SDK** (wire inspector) — `https://github.com/e2b-dev/e2b/tree/main/packages/python-sdk/e2b`
- **Code interpreter template** — `https://github.com/e2b-dev/code-interpreter/tree/main/template`

Key files inside those repos (cited throughout `docs/`):
- `infra/packages/envd/main.go:64-69` — `--isnotfc` dev flag we depend on
- `infra/packages/envd/internal/api/init.go` — `/init` handshake shape
- `infra/packages/shared/pkg/proxy/host.go:41-66` — sandbox-URL parser
- `e2b/packages/js-sdk/src/envd/versions.ts` — envdVersion branch points
- `e2b/packages/js-sdk/src/sandbox/index.ts:149-151` — where the SDK sends
  `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers

## Conventions (light)

No formal style guide yet. Defaults:

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

## Things we are NOT doing (don't accidentally add them)

Before writing code that touches any of these, check `docs/01-overview.md`
non-goals and `docs/06-phases.md`:

- Firecracker, libkrun, KVM, microVMs (deferred to Phase 6+)
- A native Go envd reimplementation (deferred to Phase 5, optional)
- Real auth (JWT/OAuth/Supabase)
- Multi-tenant, billing, quotas, teams
- Nomad, Consul, Redis, Postgres, Clickhouse
- nftables egress filtering
- Wildcard DNS setup

## Where to ask / flag

- **Conflicts with a golden rule above** → stop and ask the user.
- **Ambiguous API shape** → read the upstream source linked above; if
  still ambiguous, add a question to `docs/07-open-questions.md`.
- **Can't figure out the acceptance check for a task** →
  `docs/08-phase1-checklist.md` task entries include one; if missing,
  escalate.
