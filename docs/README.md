# edvabe — local E2B-compatible sandbox runtime

`edvabe` is a single Go binary that implements a wire-compatible subset of the
[E2B](https://e2b.dev) cloud sandbox API on a developer's laptop. Point an
E2B SDK at it via a few environment variables and sandboxes run in local
Docker containers instead of E2B's cloud — no account, no network round-trip,
no billing.

The goal is *drop-in*: applications already written against the `e2b` and
`@e2b/code-interpreter` SDKs should work unchanged against edvabe for local
development and offline testing.

## Documents

Read in this order if you are new to the project:

1. **[01-overview.md](01-overview.md)** — product goals, non-goals, target users,
   the one-sentence pitch, and the injection point that makes drop-in possible.
2. **[02-e2b-internals.md](02-e2b-internals.md)** — reference material on how
   real E2B is built (Firecracker, envd, orchestrator, client-proxy). Needed
   context for all design decisions below.
3. **[03-api-surface.md](03-api-surface.md)** — the exact REST and Connect-RPC
   surface edvabe must implement, grouped by priority tier.
4. **[04-runtime-decision.md](04-runtime-decision.md)** — Docker vs. Firecracker
   for the execution backend. Spoiler: Docker, behind a `Runtime` interface.
5. **[05-architecture.md](05-architecture.md)** — edvabe's own architecture:
   packages, in-process layout, request flow, state.
6. **[06-phases.md](06-phases.md)** — five delivery phases from "hello world
   MVP" to "byte-compatible with both SDKs", with acceptance criteria each.
7. **[07-open-questions.md](07-open-questions.md)** — design questions still to
   resolve before or during implementation.

## TL;DR

- **Name**: edvabe
- **Language**: Go
- **Deploy shape**: one static binary. `go install ./cmd/edvabe && edvabe serve`.
- **Runtime backend**: Docker/OCI containers via the local Docker socket,
  behind a `Runtime` interface. Firecracker/libkrun possible later.
- **In-sandbox agent**: upstream E2B `envd` binary (Apache-2.0) baked into
  `edvabe/base:latest`, behind an `AgentProvider` interface. Native Go
  reimplementation possible later.
- **Key architectural trick**: edvabe itself only implements the control
  plane (REST). The whole data plane — filesystem, process, PTY, watchers,
  code interpreter — is handled by envd inside the container. edvabe is a
  dumb HTTP reverse proxy for everything past the sandbox ID header.
- **Injection point for client apps**: set `E2B_API_URL`, `E2B_DOMAIN`, and
  `E2B_API_KEY` so the SDK resolves to edvabe instead of `*.e2b.app`.
- **What works in Phase 1**: full SDK hot path — create/kill sandboxes, run
  commands, read/write files, PTY, watchers, streaming stdio. Because envd
  handles the data plane, these all come for free once the reverse proxy
  works.
- **What comes later**: code interpreter, template builds, pause/resume,
  snapshots, volumes.
- **What we will not do**: multi-tenant cloud, real microVM isolation,
  billing/quotas, Supabase auth, SaaS dashboard, per-sandbox public URLs on
  real DNS.
