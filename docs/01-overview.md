# 01 — Overview

## What edvabe is

A single Go binary that accepts E2B API calls from unmodified E2B SDK
clients and services them from local Docker containers. When an app that
normally runs against `e2b.app` is pointed at edvabe via environment
variables, sandbox creation, file I/O, and command execution are handled
locally.

edvabe itself is small because it does not reimplement the whole E2B API
surface. The *control plane* (create/list/kill sandboxes, templates,
teams) is implemented as plain REST handlers in Go; the *data plane*
(filesystem, process execution, PTY, watchers, code interpreter) is
handled by the upstream E2B `envd` binary running inside each sandbox's
container. edvabe only needs to start containers, wire a reverse proxy,
and forward HTTP bytes.

The shape the user sees: install one binary, run `edvabe serve`, set three
environment variables in their app, done.

## Why

E2B is a good product but painful to rely on during iteration:

- **Network round-trips** to `api.e2b.app` slow down every agent test cycle.
- **Rate limits / quotas** get hit during hot iteration loops.
- **Offline work** is impossible.
- **Billing** adds up when a long-running agent churns through sandboxes.
- **Self-hosting** the real E2B infra (`e2b-dev/infra`) requires GCP, Nomad,
  Consul, Clickhouse, Grafana/Loki/Tempo/Mimir, Supabase, Firecracker, KVM,
  NBD, UFFD, etc. It is production-shaped and developer-hostile.

A local drop-in lets the existing E2B SDKs be the interface while the heavy
cloud machinery is replaced with a container-per-sandbox runtime that every
developer already has. The in-container agent (`envd`) is reused unchanged
from upstream, so we get byte-exact wire compatibility for free.

## Target users

- App developers who use `e2b` or `@e2b/code-interpreter` in code-execution
  agents and want fast iteration loops on their laptop.
- Test/CI environments that want deterministic, offline sandbox behavior.
- Teams self-hosting who do not want to run a Firecracker fleet.

Explicitly **not**:

- Multi-tenant cloud hosting — edvabe is trust-the-caller software.
- Hardened isolation for adversarial workloads. The threat model assumes the
  user is running code they (or their agent) generated.

## Success criteria

edvabe is "done" (v1) when:

1. A JS/Python program that works against `api.e2b.app` works unchanged after
   setting three environment variables to point at a local edvabe.
2. `Sandbox.create()`, `sandbox.commands.run()`, `sandbox.files.write/read()`,
   `sandbox.kill()` all return byte-compatible payloads for common cases.
3. `@e2b/code-interpreter`'s `runCode()` with Python and JavaScript contexts
   returns the same `Execution` shape as real E2B, including rich outputs.
4. First sandbox create on a warm image is under 1 second on a developer
   laptop.
5. The binary works on Linux x86_64, Linux arm64, macOS arm64 (Apple Silicon),
   and macOS x86_64. Windows is nice-to-have via WSL2.

## The injection point

E2B SDKs already have dev/debug escape hatches we exploit. From the SDK source
(see [03-api-surface.md](03-api-surface.md) for citations):

- `E2B_API_URL` — control plane base URL. Default `https://api.e2b.app`.
- `E2B_DOMAIN` — domain used to build per-sandbox data-plane URLs. Default
  `e2b.app`.
- `E2B_DEBUG=true` — short-circuit: use `localhost:3000` for control plane and
  `localhost:49983` for envd, no subdomains.
- `E2B_SANDBOX_URL` — override the data-plane base URL.
- `E2B_ACCESS_TOKEN` / `E2B_API_KEY` — accept any non-empty string; we do no
  real auth.
- The JS SDK always sends `E2b-Sandbox-Id` and `E2b-Sandbox-Port` headers in
  addition to the subdomain, so we can route by header and skip wildcard DNS
  entirely.

A user's setup looks like:

```sh
export E2B_API_URL=http://localhost:3000
export E2B_DOMAIN=localhost:3000
export E2B_API_KEY=edvabe_local
./edvabe serve --port 3000
```

The SDK does the rest.

## Non-goals

Things we **will not** implement, in some cases ever:

- Firecracker microVMs in v1 (see [04-runtime-decision.md](04-runtime-decision.md)).
- A native Go reimplementation of envd in v1 (see Phase 5 in
  [06-phases.md](06-phases.md)). The upstream envd binary does the job.
- Live-memory snapshot/resume. Pause/resume is approximated with
  `docker pause` + committed images, which is behaviorally close enough.
- NBD/UFFD-backed rootfs; chunked diff storage; NFS volume gateway.
- Multi-node orchestration; cluster mode; Redis catalog; Nomad.
- Real user/team/billing management. Auth is stub-accept-anything.
- The Supabase-backed dashboard.
- Egress allowlist/denylist enforced via nftables on the host.
- A proprietary template format. Templates are Docker images.

Saying "no" to these is how edvabe stays a single binary.
