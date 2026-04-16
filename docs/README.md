# edvabe — documentation

## Design documents

1. **[01-overview.md](01-overview.md)** — product goals, non-goals, target users.
2. **[02-e2b-internals.md](02-e2b-internals.md)** — how real E2B is built
   (Firecracker, envd, orchestrator). Context for design decisions.
3. **[03-api-surface.md](03-api-surface.md)** — the exact REST and Connect-RPC
   surface edvabe implements, grouped by priority tier.
4. **[04-runtime-decision.md](04-runtime-decision.md)** — Docker vs. Firecracker.
   Spoiler: Docker, behind a `Runtime` interface.
5. **[05-architecture.md](05-architecture.md)** — packages, request flow, state.
6. **[06-phases.md](06-phases.md)** — feature scope by phase, with acceptance
   criteria.
7. **[07-open-questions.md](07-open-questions.md)** — resolved design questions.

## TL;DR

- **Language**: Go. One static binary: `go install ./cmd/edvabe && edvabe serve`.
- **Runtime**: Docker containers via the local socket, behind a `Runtime`
  interface. Firecracker/libkrun possible later.
- **Data plane**: upstream `envd` binary inside each container, reverse-proxied
  by edvabe. edvabe does not reimplement filesystem/process/PTY/watchers.
- **Control plane**: Go HTTP handlers for sandboxes, templates, teams, volumes,
  api-keys, etc.
- **Injection point**: set `E2B_API_URL`, `E2B_DOMAIN`, `E2B_API_KEY`, and
  `E2B_SANDBOX_URL` so the SDK resolves to edvabe instead of `*.e2b.app`.
- **Not in scope**: multi-tenant cloud, real microVM isolation, billing/quotas,
  Supabase auth.
