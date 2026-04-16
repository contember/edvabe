# 06 — Feature scope

Phases 1–4 are shipped. Phase 5+ is optional and built on demand.

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
- `edvabe pull-base`
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

---

## Phase 3 — "Templates and pause"

**Goal**: users can build custom templates using the E2B JS SDK's
programmatic `Template()` builder (`template.ts` + `Template.build(...)`)
and pause/snapshot sandboxes to save state. The driving consumer is
webmaster, which defines templates in TypeScript (see
`webmaster/containers/templates/chrome/template.ts`) and builds them
against edvabe's control plane.

### Scope (T3 endpoints)

#### Template builds

The SDK's `Template.build()` performs a three-step wire dance the server
must cooperate with: create template record, upload file contexts by
content hash, start a build with a JSON step array. Each piece is
required.

**Endpoints:**

- `POST /v3/templates` — create template record. Body
  `{name, tags[], cpuCount?, memoryMB?, skipCache?}`; returns
  `{templateID, buildID, names, tags, aliases, public}`. `name` is the
  user-friendly handle (e.g. `webmaster-sandbox-chrome` or
  `my-tmpl:v1`); it serves as both alias and tag.
- `GET /templates/{templateID}/files/{hash}` — returns
  `{present: bool, url?: string}`. If not in the local file cache,
  return a `url` pointing at edvabe's own upload handler (see below).
- `POST /_upload/{hash}` *(edvabe-internal, served by the same listener
  but outside the E2B API surface)* — accepts the tar body from the SDK
  and writes it into the content-addressed store. Token-gated by a
  short-lived HMAC that `GET .../files/{hash}` embeds in the returned
  `url`.
- `POST /v2/templates/{templateID}/builds/{buildID}` — start the build.
  Body `TemplateBuildStartV2 {fromImage?, fromTemplate?,
  fromImageRegistry?, steps:[{type, args, filesHash, force}],
  startCmd?, readyCmd?, force?}`. Returns 202 and kicks off the async
  builder goroutine.
- `GET /templates/{templateID}/builds/{buildID}/status` — polled by the
  SDK. Returns `{status: "waiting"|"building"|"ready"|"error", reason?}`.
- `GET /templates/{templateID}/builds/{buildID}/logs?cursor=&limit=&direction=&level=&source=`
  — paginated structured build logs. For MVP honour `cursor`, `limit`,
  `direction`; silently accept `level` and `source` without filtering.
- `GET /templates`, `GET /templates/{templateID}` (with
  `TemplateWithBuilds` shape), `DELETE /templates/{templateID}`,
  `PATCH /v2/templates/{templateID}` (accepts `{public, tags?}`; public
  is stored but ignored).
- `GET /templates/aliases/{alias}` — resolves an alias to
  `{templateID, public}`.

**Step → Dockerfile translation.** The SDK serialises builder methods
into an ordered `TemplateStep[]`. Map each step type onto a line of a
generated Dockerfile in `~/.cache/edvabe/builds/<buildID>/Dockerfile`,
then run `docker build` against that context:

| Step `type`       | Dockerfile output                                         |
|-------------------|------------------------------------------------------------|
| (implicit first)  | `FROM <fromImage>` or `FROM edvabe/user-<fromTemplate>:latest` |
| `run`             | `RUN <args>` (shell form)                                  |
| `copy`            | `COPY <extracted-hash-dir>/<src> <dest>`                   |
| `copyItems`       | one `COPY` per item                                        |
| `workdir`         | `WORKDIR <arg>`                                            |
| `user`            | `USER <arg>`                                               |
| `mkdir`           | `RUN mkdir -p <args>`                                      |
| `symlink`         | `RUN ln -s <src> <dest>`                                   |
| `remove`          | `RUN rm -rf <args>`                                        |
| `rename`          | `RUN mv <src> <dest>`                                      |
| `aptInstall`      | `RUN apt-get update && apt-get install -y --no-install-recommends <pkgs> && rm -rf /var/lib/apt/lists/*` |
| `pipInstall`      | `RUN pip install --no-cache-dir <pkgs>`                    |
| `npmInstall`      | `RUN npm install -g <pkgs>`                                |
| `skipCache`       | prepend `ARG EDVABE_CACHE_BUST_<n>=<uuid>` to the next step |

`startCmd` and `readyCmd` are **not** written into the Dockerfile —
they're persisted in the template metadata store and applied at sandbox
create time (see "Sandbox create integration" below).

The builder interprets steps into a Dockerfile itself — there is no
"accept a literal Dockerfile" compatibility shim. Users who want raw
Dockerfile control use `toDockerfile()` client-side and feed it through
the same builder API.

**File-context cache.** Content-addressed store rooted at
`~/.cache/edvabe/template-files/<hash>.tar` (configurable via
`EDVABE_CACHE_DIR`). `GET .../files/{hash}` returns `{present: true}`
when the file exists, otherwise `{present: false, url: "<upload-url>"}`.
The SDK tarballs the source paths client-side and uploads them; edvabe
writes them atomically (`*.part` → rename). During build, the tar for
each step's `filesHash` is extracted into
`~/.cache/edvabe/builds/<buildID>/ctx/<hash>/` so the generated
Dockerfile can `COPY` from there.

**envd injection.** User templates start from arbitrary base images
(e.g. `oven/bun:slim`) that do not ship envd. The builder must
transparently append a final stage to every generated Dockerfile:

```dockerfile
COPY --from=edvabe/envd-source /usr/local/bin/envd /usr/local/bin/envd
COPY --from=edvabe/envd-source /usr/local/bin/edvabe-init /usr/local/bin/edvabe-init
```

where `edvabe/envd-source` is a scratch image edvabe builds once at
startup (same envd binary as Phase 1's `edvabe/base:latest`), and
`edvabe-init` is a tiny shell wrapper (see below). The final image's
`CMD` is rewritten to `["/usr/local/bin/edvabe-init"]` regardless of
what the user set; the user's `startCmd` is preserved in metadata and
re-executed by `edvabe-init` at container start.

**`edvabe-init` entrypoint wrapper.** `envd` must be the container's
long-lived process because that's what the reverse proxy talks to.
User-defined `startCmd` needs to run alongside it (the chrome template's
`.chrome-start.sh` is load-bearing — without it Chrome never boots).
`edvabe-init` is a ~15-line sh script:

```sh
#!/bin/sh
/usr/local/bin/envd --isnotfc &
ENVD_PID=$!
if [ -n "$EDVABE_START_CMD" ]; then
    sh -c "$EDVABE_START_CMD" &
fi
wait $ENVD_PID
```

`EDVABE_START_CMD` and `EDVABE_READY_CMD` are injected into the
container's env via `HostConfig.Env` at `docker run` time from the
template metadata. Subprocess supervision is deliberately dumb — if the
user's start command dies, envd stays up and the sandbox stays
reachable. If envd dies, the container exits and the sandbox manager
notices on its next healthcheck.

**`readyCmd` handling.** If the template metadata has a non-empty
`readyCmd`, the sandbox manager runs it via envd's process RPC after
`InitAgent` succeeds and before `POST /sandboxes` returns 201 to the
client. Poll loop: execute `readyCmd`, expect exit 0 within the sandbox
timeout budget, retry with 500ms backoff. If it never returns 0,
respond 504 and kill the container. No `readyCmd` → no wait, same
behaviour as Phase 1.

**`fromTemplate` chaining.** When `TemplateBuildStartV2.fromTemplate` is
set, resolve it via the template store (alias or UUID) and emit
`FROM edvabe/user-<parentTemplateID>:latest` as the base. The parent
build must be in `ready` state; otherwise return 409 before starting
the build.

**`fromImageRegistry` credentials.** Accept the credentials object and
forward it to `docker build` via the Docker API's auth config. This is
load-bearing only for private registries — for webmaster's public
`oven/bun:slim` base it's a passthrough.

**Async builder state machine.** One goroutine per active build, owned
by a `BuildManager` with a `map[buildID]*Build` under a mutex. States:

```
waiting  → set when POST /v2/templates/{id}/builds/{bid} is received
            and enqueued (effectively instantaneous in local mode)
building → set when the goroutine starts docker build
ready    → set on successful completion, tag image as
            edvabe/user-<templateID>:latest
error    → set on docker build failure; capture exit message in `reason`
```

Log entries are appended to an in-memory ring buffer (bounded, e.g.
5000 lines per build) as `docker build` stdout/stderr comes in, each
entry timestamped. Older entries drop off the tail. The logs endpoint
reads from this buffer under the same mutex.

**Template metadata store.** Persisted to
`~/.local/share/edvabe/templates.json` (or BoltDB — pick whichever is
simpler; JSON is fine for v1). Schema per entry: `{templateID, name,
tags[], alias, cpuCount, memoryMB, startCmd, readyCmd, imageTag,
createdAt, builds:[{buildID, status, reason?, startedAt, finishedAt?}]}`.
Read at `edvabe serve` startup into an in-memory map; writes go
through the same mutex as the BuildManager and are flushed
synchronously. Without this, a restart of `edvabe serve` would strand
every built template (docker image survives, but the alias mapping and
startCmd would be lost).

**Sandbox create integration.** `POST /sandboxes` must accept
`templateID` as either a UUID or an alias/name and resolve it to an
image tag via the template store:

1. Look up by UUID in `templates[]`.
2. Failing that, look up by name/alias.
3. Failing that, treat it as the literal `"base"` sentinel →
   `edvabe/base:latest` (Phase 1 behaviour).

Once resolved, `Runtime.Create` is called with the resolved image tag
plus `EDVABE_START_CMD` / `EDVABE_READY_CMD` env injected from
template metadata. The sandbox manager then runs `readyCmd` probe
before returning. This is the glue that makes
`Sandbox.create(template='webmaster-sandbox-chrome')` actually launch
the right container.

**Resource fields (`cpuCount`, `memoryMB`).** Stored on the template
record for wire completeness, **not enforced** by the runtime. Document
the caveat in `edvabe doctor` output.

**Multi-arch.** Builds target the host architecture only. No
`--platform` support. Document as a known limitation.

#### Pause, snapshots, connect

- `POST /sandboxes/{id}/snapshots` → `docker commit` to
  `edvabe/snap-<id>:latest`, return `SnapshotInfo`.
- `POST /sandboxes/{id}/pause` → `docker pause`.
- `POST /sandboxes/{id}/connect` handles the "was paused" branch via
  `docker unpause`.
- `NewSandbox.autoPause` and
  `NewSandbox.lifecycle.onTimeout == "pause"` on `POST /sandboxes`:
  when set, the sandbox manager calls `docker pause` on timeout
  instead of `docker rm --force`, and `/connect` resumes via
  `docker unpause`. This is load-bearing for the webmaster consumer
  (`BaseE2BSandbox.betaCreate(template, { autoPause: true, … })` in
  `packages/worker/src/lib/sandbox/e2b-sandbox.ts`). Without it the
  idle-pause + reconnect loop breaks the first time the sandbox times
  out.
- `Sandbox.betaCreate` / `Sandbox.betaPause` wire shapes — verify they
  match the `POST /sandboxes` and `POST /sandboxes/{id}/pause` the
  stable SDK paths produce before implementing, since webmaster uses
  the beta flavor.

#### Logs and metrics

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

Two flows must work end-to-end.

**Flow A — programmatic Template SDK (webmaster-equivalent):**

```ts
import { Template, defaultBuildLogger } from 'e2b'
import { Sandbox } from 'e2b'

const template = Template()
    .fromImage('oven/bun:slim')
    .aptInstall(['curl', 'ca-certificates'])
    .runCmd('echo "hello from build" > /etc/greeting')
    .setUser('root')
    .setStartCmd('sleep infinity')

await Template.build(template, {
    alias: 'edvabe-smoke',
    memoryMB: 1024,
    onBuildLogs: defaultBuildLogger(),
})

const sbx = await Sandbox.create('edvabe-smoke')
const greeting = await sbx.commands.run('cat /etc/greeting')
assert(greeting.stdout.trim() === 'hello from build')
await sbx.pause()
const sbx2 = await Sandbox.connect(sbx.sandboxId)
const result2 = await sbx2.commands.run('echo resumed')
assert(result2.exitCode === 0)
```

**Flow B — the real `webmaster-sandbox-chrome` template** built against
edvabe from `webmaster/containers/templates/chrome/build.ts` unchanged,
then `Sandbox.create('webmaster-sandbox-chrome')` launches a working
Chrome sandbox. This is the acceptance bar for "Phase 3 ships".

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

