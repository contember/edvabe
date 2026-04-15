# 06 — Delivery phases

Five phases plus one optional follow-up. Each phase is itself shippable —
useful to at least one category of user — before the next phase starts.

The envd-passthrough approach collapses what used to be two phases into
Phase 1. Because edvabe does not reimplement filesystem/process/PTY/watch
operations (envd does), they all work the moment the reverse proxy works.
That's the whole reason this decomposition is shorter than earlier drafts.

---

## Phase 1 — "Single binary runs everything"

**Goal**: a user runs `edvabe serve` and their existing unmodified Python
or JS E2B app can do the full hot path: create sandbox → run commands →
read/write files → stream stdout → open PTYs → watch directories → kill.
Code interpreter excluded. Templates beyond `base` excluded.

### Scope

**Control plane REST** (T0 endpoints):

- `GET /health`
- `POST /sandboxes` (create)
- `GET /sandboxes/{id}` (get)
- `GET /v2/sandboxes` (paginated list)
- `DELETE /sandboxes/{id}` (kill)
- `POST /sandboxes/{id}/timeout`
- `POST /sandboxes/{id}/connect` (reconnect)

**Runtime** (`internal/runtime/docker/`):

- Docker socket discovery (tries common paths).
- `Create`: ContainerCreate + ContainerStart from `edvabe/base:latest`.
- `Destroy`: ContainerRemove with Force.
- `Stats`: ContainerStats → struct.
- `AgentEndpoint`: ContainerInspect → bridge IP + 49983.

**Agent provider** (`internal/agent/upstream/`):

- Download envd binary from GitHub releases (cache in `~/.cache/edvabe/envd/`)
- `EnsureImage`: if missing, generate build context from embedded
  Dockerfile + cached envd binary, call `docker build -t edvabe/base:latest`
- `InitAgent`: HTTP POST to envd's `/init` with access token, env vars,
  default user, workdir
- `Ping`: GET /health with retry loop until agent is ready

**Reverse proxy** (`internal/api/proxy.go`):

- `httputil.ReverseProxy` whose Director looks up sandbox by
  `E2b-Sandbox-Id` header and rewrites the URL to
  `http://<bridge-ip>:<port>`.
- Preserves Connect-RPC framing (just passes bytes).
- Preserves streaming responses (no buffering).
- Preserves request/response headers except for hop-by-hop.

**Sandbox manager**:

- in-memory `map[string]*Sandbox` with a mutex.
- ID/token minting (`isb_<ulid>`, `ea_<random>`, `ta_<random>`).
- TTL watchdog goroutine (one per sandbox, or one central timer wheel).
- `Connect` handler for reconnect.

**Dispatch**:

- Parse `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers, fall back to
  `Host: <port>-<id>.<rest>` parsing.
- No sandbox context → control router. Sandbox context → proxy.

**Auth**:

- `X-API-Key` required-but-not-validated on control plane.
- Envd plane auth is handled by envd itself.

**CLI**:

- `edvabe serve [--port 3000] [--docker-socket ...]`
- `edvabe doctor`
- `edvabe build-image [--force]`
- `edvabe fetch-envd [--version ...]`
- `edvabe version`

**Base image** (`assets/Dockerfile.base`):

- Ubuntu 22.04 + Python 3 + Node 20 + common shell tools
- Upstream `envd` binary at `/usr/local/bin/envd`
- `CMD ["/usr/local/bin/envd", "--isnotfc"]`
- Non-root `user` account with passwordless sudo

### Explicitly not in scope (Phase 1)

- **Code interpreter overlay** — Phase 2
- **Template builds** — Phase 3
- **Pause/resume/snapshots** — Phase 3
- **Volumes, teams, api-keys, admin** — Phase 4
- **Metrics endpoints** — Phase 3 (can stub if needed)
- **User-port forwarding** (`<port>-<id>.localhost` for user-bound services)
  — basic support included as part of the reverse proxy, tested in Phase 2

### Acceptance criterion

```python
# run with
#   E2B_API_URL=http://localhost:3000
#   E2B_DOMAIN=localhost:3000
#   E2B_API_KEY=dev
# after `edvabe serve`
from e2b import Sandbox

sbx = Sandbox.create(timeout=60)

# commands
result = sbx.commands.run("echo hello from edvabe")
assert result.stdout.strip() == "hello from edvabe"
assert result.exit_code == 0

# filesystem
sbx.files.write("/home/user/foo.txt", "hello")
assert sbx.files.read("/home/user/foo.txt") == "hello"

entries = sbx.files.list("/home/user")
assert any(e.name == "foo.txt" for e in entries)

# PTY
handle = sbx.commands.run("bash", pty=True, background=True)
handle.send_stdin("echo in pty\n")
# ...assertions on output

# watch
with sbx.files.watch_dir("/home/user") as events:
    sbx.files.write("/home/user/bar.txt", "x")
    evt = next(events)
    assert evt.name == "bar.txt"

sbx.kill()
```

...and the equivalent TypeScript test using `@e2b/sdk`. Plus:

- `edvabe doctor` reports green on a clean macOS + OrbStack install.
- `edvabe doctor` reports green on a clean Linux + Docker install.
- First-run image build completes in under 60 seconds on a fast connection.
- Second-run `edvabe serve` starts in under 1 second.
- First `Sandbox.create` after warm start completes in under 2 seconds.

### Expected LOC

~1500–2500 lines of Go (control plane handlers + runtime + agent provider
+ manager + proxy + CLI), plus ~100 lines of embedded Dockerfile and asset
shims.

This is bigger than the original Phase 1 estimate because we are shipping
what used to be Phase 1 + Phase 2 together. But it is smaller than the
combined estimate because edvabe itself does not reimplement the data
plane — envd does.

---

## Phase 2 — "Code interpreter"

**Goal**: `@e2b/code-interpreter` / `e2b_code_interpreter` SDKs work
end-to-end, including rich outputs (matplotlib PNG, pandas DataFrame,
plotly HTML).

### Scope (T2 endpoints)

- Build `edvabe/code-interpreter:latest` Docker image derived from the
  upstream `code-interpreter/template/` + envd binary. Replace the systemd
  units with supervisord (or tini with two foreground processes: jupyter
  server + FastAPI wrapper) so it runs cleanly in a plain container.
- New agent provider configuration: same `UpstreamEnvdProvider` but with
  a different base image tag.
- Dispatch: when `E2b-Sandbox-Port == 49999`, proxy to the container's
  port 49999 instead of 49983. No new code — the reverse proxy already
  handles arbitrary ports.
- New template registration: `code-interpreter-v1` →
  `edvabe/code-interpreter:latest`.
- `edvabe build-image --template=code-interpreter` adds a second image to
  the bootstrap step.

### Implementation notes

The simplest path is to **not reimplement the FastAPI wrapper**. Bake the
upstream Python server into the image; edvabe proxies `/execute` and
`/contexts` requests through to `http://<container-ip>:49999/...`. NDJSON
streaming passes through byte-exact via `io.Copy`.

We inherit any quirks of the upstream server for free, which is fine.

### Acceptance criterion

```python
from e2b_code_interpreter import Sandbox

sbx = Sandbox.create()
execution = sbx.run_code("""
import matplotlib.pyplot as plt
plt.plot([1,2,3], [4,5,6])
plt.savefig("/tmp/x.png")
print("done")
""")
assert execution.logs.stdout[0] == "done"

execution = sbx.run_code("1+1")
assert execution.results[0].text == "2"

execution = sbx.run_code("""
import pandas as pd
pd.DataFrame({"a":[1,2,3]})
""")
assert "html" in execution.results[0].__dict__

sbx.kill()
```

Plus the TypeScript equivalent.

### Expected LOC

~300 additional lines of Go (mostly dispatch + new image registration).
The heavy lifting is in the Dockerfile.

---

## Phase 3 — "Templates and pause"

**Goal**: users can build custom templates from their own Dockerfiles and
pause/snapshot sandboxes to save state.

### Scope (T3 endpoints)

- `POST /v3/templates` → `POST /v2/templates/{id}/builds/{buildID}`:
  - Accept `TemplateBuildStartV2` with steps, `startCmd`, `readyCmd`.
  - Translate the step list into a generated Dockerfile or accept a literal
    one via the compatibility shim.
  - `docker build` with a tee to an in-memory log buffer.
  - Tag as `edvabe/user-<templateID>:latest`.
- `POST /sandboxes/{id}/snapshots` → `docker commit` to
  `edvabe/snap-<id>:latest`, return `SnapshotInfo`.
- `POST /sandboxes/{id}/pause` → `docker pause`.
- `POST /sandboxes/{id}/connect` handles the "was paused" branch via
  `docker unpause`.
- `GET /templates`, `GET /templates/{id}`,
  `GET /templates/{id}/builds/{buildID}/status`,
  `GET /templates/{id}/builds/{buildID}/logs`,
  `PATCH /v2/templates/{id}`, `DELETE /templates/{id}`,
  `GET /templates/aliases/{alias}`.
- `GET /v2/sandboxes/{id}/logs` — paginated viewer sourced from envd's
  own log endpoints (if exposed) or the container's stdout.
- `GET /sandboxes/{id}/metrics`, `GET /sandboxes/metrics?sandbox_ids=...`
  — sourced from Docker stats.

### Pause semantics caveat

Make the trade-off explicit in CLI help text and docs: "pause" in edvabe
freezes the container's processes (like SIGSTOP). It does not survive an
`edvabe serve` restart. For persistent state, snapshot
(`docker commit`) saves the filesystem but not running memory — resume
runs the template's CMD again.

### Acceptance criterion

```python
# Build a template
template = sbx_api.build_template(
    dockerfile="FROM python:3.11\nRUN pip install requests\n",
    name="my-tmpl",
)

sbx = Sandbox.create(template=template.id)
result = sbx.commands.run("python -c 'import requests; print(requests.__version__)'")
assert result.exit_code == 0

# Pause and reconnect
sbx.pause()
sbx2 = Sandbox.connect(sbx.id)
assert sbx2.id == sbx.id
result2 = sbx2.commands.run("echo resumed")
```

### Expected LOC

~1000 additional lines of Go + a template builder subpackage.

---

## Phase 4 — "Full surface"

**Goal**: every E2B SDK call receives a response that matches the shape
real E2B returns, even if the behavior is stubbed. The SDK should never
crash or fall back to an error branch on an endpoint it expects to exist.

### Scope (T4 endpoints)

- `/teams`, `/teams/{id}/metrics`, `/teams/{id}/metrics/max` — single
  hard-coded "local" team.
- `/api-keys`, `/access-tokens` — in-memory CRUD on a fake registry.
- `/volumes`, `/volumes/{id}` — in-memory registry; volume content
  accessible via bind mounts at `/volumes/<name>` inside sandboxes.
- `/volumecontent/...` — thin HTTP wrapper over the host directory
  backing each volume (optional).
- `/nodes`, `/admin/*` — return 501 or empty arrays.
- `PUT /sandboxes/{id}/network` — record state, do not enforce.
- Deprecated endpoints as aliases: `POST /sandboxes/{id}/resume`,
  `POST /templates`, `PATCH /templates/{id}` (v1), legacy logs.
- `network.allowPublicTraffic` traffic-token enforcement on the reverse
  proxy.
- `NewSandbox.mcp` → route to a different default template and run
  `mcp-gateway --config '<json>'` as first command.
- `clientID` and `alias/aliases` fields populated for legacy SDK
  compatibility.

### Acceptance criterion

Run the upstream E2B SDK test suite (both JS and Python) against edvabe.

- **Pass**: all tests for sandbox lifecycle, filesystem, commands, PTY,
  watchers, code interpreter, templates, pause/resume.
- **Skip or expected-fail**: multi-tenant, billing, Supabase auth, admin
  endpoint tests.
- **No crashes**: every test the SDK invokes returns a parseable response,
  even if "not implemented."

### Expected LOC

~1200 additional lines, mostly stubs.

---

## Phase 5 — "Native agent" (optional)

**Goal**: replace the upstream envd binary with a native Go
reimplementation compiled into edvabe. Only worth doing if at least one of:

- Upstream envd starts breaking behavior we depend on.
- Running envd as a separate binary inside a container becomes a
  debugging/deployment bottleneck.
- We want to support running sandboxes *without Docker* — e.g. as
  subprocesses of edvabe on the host, for a really lightweight mode.

### Scope

- `internal/agent/native/` implements `AgentProvider`.
- Reimplement envd's Connect-RPC services (`process.Process`,
  `filesystem.Filesystem`) in Go using `connectrpc.com/connect`.
- Reimplement the REST endpoints (`/init`, `/health`, `/envs`, `/metrics`,
  `/files`).
- Ship as either:
  - (a) A second binary baked into an alternative base image.
  - (b) A static binary embedded into edvabe via `//go:embed`, extracted
    to a bind mount at startup.
  - (c) An in-process Go package that edvabe runs as a goroutine when the
    runtime is "subprocess" (no container) — a whole new Runtime impl.

### Why we might never need this

If upstream envd remains stable and works well inside plain Docker
containers (which it already does, via `--isnotfc`), there is no user-
visible reason to replace it. Keep the interface open; do not build
until forced.

---

## Phase 6+ — "Beyond"

Out of scope for v1 but worth keeping architecturally possible:

- **Firecracker runtime** (`internal/runtime/firecracker/`) — slotted in
  behind the existing `Runtime` interface. Linux + `/dev/kvm` only. Gates
  behind `--runtime=firecracker`. Would reuse the same upstream envd
  agent.
- **libkrun runtime** — cross-platform microVM, the more likely second
  runtime backend than Firecracker (Apple Silicon support matters). See
  [microsandbox](https://github.com/microsandbox/microsandbox).
- **gVisor runtime shim** — `--runtime=runsc` flag; just forwards to
  Docker with `--runtime=runsc`. One-line change for Linux users who want
  a user-space kernel.
- **Persistent state** — BoltDB/SQLite sidecar for sandbox registry so
  `edvabe serve` restarts preserve handles.
- **Real egress filtering** — nftables chain per sandbox.
- **Multi-user mode** — real auth, separate namespaces.

---

## Cross-phase engineering

These show up in every phase and should not be deferred:

- **Conformance suite** in `internal/conformance/` that runs against a
  live edvabe and asserts byte-compatible responses. Grows per phase.
- **Reference traces** — captured HTTP request/response pairs from real
  E2B calls, committed as golden files under `testdata/traces/`. Each
  phase adds traces for the endpoints it covers.
- **Error envelope consistency** — shared formatter from the start.
- **Structured logging** with request IDs.
- **Benchmarks**: sandbox create latency, exec throughput, upload/download
  throughput.
- **Pinned `envdVersion`** = `"0.5.7"` from Phase 1 so no SDK branches into
  legacy paths.

## Timeline gut-feel

Solo developer, half-time:

- Phase 1: 2–4 weeks (biggest phase because it includes envd-passthrough plumbing)
- Phase 2: 1–2 weeks
- Phase 3: 3–4 weeks
- Phase 4: 2–3 weeks
- **Total v1: 2–3 months half-time, 4–6 weeks full-time**

Phase 5 (native agent) is open-ended and only built on demand.
Firecracker/libkrun (Phase 6+) is another 6–8 weeks on top.

The passthrough approach roughly halves the original estimate by moving
filesystem/process/PTY/watch work out of edvabe entirely.
