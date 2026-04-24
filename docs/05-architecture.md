# 05 — edvabe architecture

How the edvabe Go binary is laid out internally. This is a design sketch,
not a fixed structure — it may evolve during Phase 1 implementation.

## Two swappable axes

edvabe's architecture has two independent pluggable axes:

1. **Runtime** — *where* a sandbox actually runs. Phase 1 = Docker containers
   via the local Docker socket. Later: Firecracker, libkrun, gVisor.
2. **Agent** — *what* serves the envd wire protocol inside a sandbox.
   Phase 1 = the upstream E2B `envd` binary baked into the container image.
   Later: a native Go reimplementation, or a shared library compiled into
   edvabe itself.

These are orthogonal: any Runtime can host any Agent, because both agree on
the contract "the agent listens on a known TCP port inside the sandbox and
speaks the envd wire protocol." The Runtime's job is just to put the
sandbox somewhere and tell edvabe how to reach the agent. edvabe then
reverse-proxies all envd traffic to it.

**edvabe itself never speaks envd's Connect-RPC protocol.** It only
implements the control plane (REST) and dispatches everything else to the
agent through a reverse proxy. That's the whole architectural trick that
keeps the binary small.

## High-level diagram

```
┌──────────────────────────────────────────────────────────────┐
│                       edvabe process                         │
│                                                              │
│   ┌────────────┐      ┌─────────────┐                        │
│   │ HTTP :3000 │─────▶│  Dispatch   │                        │
│   └────────────┘      │ (by header) │                        │
│                       └──┬────────┬─┘                        │
│                          │        │                          │
│           Control-plane  │        │  Data-plane + CI         │
│           REST           │        │  (passthrough)           │
│                          ▼        ▼                          │
│                ┌──────────────┐ ┌─────────────┐              │
│                │ Control API  │ │  Reverse    │              │
│                │ handlers     │ │  Proxy      │              │
│                └──────┬───────┘ └──────┬──────┘              │
│                       │                │                     │
│                       ▼                │                     │
│                ┌──────────────┐        │                     │
│                │ Sandbox Mgr  │◀───────┘ (lookup by ID)       │
│                │ map[id]*Sbx  │                               │
│                └──────┬───────┘                               │
│                       │                                       │
│                       ▼                                       │
│                ┌──────────────┐   ┌───────────────┐           │
│                │   Runtime    │──▶│ AgentProvider │           │
│                │   (Docker)   │   │ (Upstream     │           │
│                │              │   │  envd)        │           │
│                └──────┬───────┘   └───────┬───────┘           │
└───────────────────────┼───────────────────┼───────────────────┘
                        │                   │
                        ▼                   │
             /var/run/docker.sock           │
                        │                   │
                        ▼                   ▼
                 Docker daemon       (builds base image with
                        │             upstream envd baked in)
                        ▼
             ┌──────────────────────┐
             │   Container per      │
             │   sandbox            │
             │  ┌────────────────┐  │
             │  │ envd :49983    │  │◀── reverse proxy forwards here
             │  │ (upstream)     │  │
             │  └────────────────┘  │
             └──────────────────────┘
```

Single process. Single port. No Redis, no database, no background workers
in v1.

## Runtime interface

Lives at `internal/runtime/runtime.go`. Much smaller than the version in
earlier drafts because the data-plane operations (exec, files, watchers)
are handled by the in-container agent, not by edvabe.

```go
type Runtime interface {
    // Name for logging and --runtime= flag.
    Name() string

    // Create and start a sandbox. Returns a handle the manager stores.
    Create(ctx context.Context, req CreateRequest) (*SandboxHandle, error)

    // Stop and remove.
    Destroy(ctx context.Context, sandboxID string) error

    // Freeze / unfreeze processes (Phase 4).
    Pause(ctx context.Context, sandboxID string) error
    Unpause(ctx context.Context, sandboxID string) error

    // Persist filesystem state as a new template image (Phase 4).
    Commit(ctx context.Context, sandboxID, imageTag string) error

    // Resource usage for metrics endpoints.
    Stats(ctx context.Context, sandboxID string) (*Stats, error)

    // Build a user template image from a build request (Phase 4).
    BuildImage(ctx context.Context, req BuildRequest) error

    // Tell the reverse proxy where this sandbox's agent lives.
    AgentEndpoint(sandboxID string) (host string, port int, err error)
}

type CreateRequest struct {
    SandboxID    string
    Image        string            // e.g. "edvabe/base:latest"
    EnvVars      map[string]string
    Metadata     map[string]string
    Timeout      time.Duration
    AgentPort    int               // 49983 for envd
    AgentToken   string            // access token edvabe minted
    BindMounts   map[string]string // host path -> container path
}

type SandboxHandle struct {
    ContainerID string
    AgentHost   string   // e.g. Docker bridge IP
    AgentPort   int      // usually 49983
    CreatedAt   time.Time
}
```

Notice what's **not** here: `Exec`, `ReadFile`, `WriteFile`, `Stat`,
`ListDir`, `Watch`, `ForwardPort`. Those are all agent concerns. The
Runtime just does container lifecycle.

## Agent provider interface

Lives at `internal/agent/agent.go`. Defines how an agent (the thing
speaking envd protocol inside the sandbox) is provisioned and initialized.

```go
type AgentProvider interface {
    // Name for logging and --agent= flag.
    Name() string

    // Version string to report as `envdVersion` in Sandbox response.
    // SDKs branch on this; we pin to "0.5.7" or higher.
    Version() string

    // Port the agent listens on inside the container.
    Port() int

    // Ensure a base image tagged as `tag` exists on the runtime and
    // contains the agent binary + runtime dependencies. Idempotent.
    // Called at edvabe startup or on first sandbox create.
    EnsureImage(ctx context.Context, runtime Runtime, tag string) error

    // Call the agent's /init endpoint after the sandbox has started.
    // Hands the agent its access token, env vars, default user/workdir.
    InitAgent(ctx context.Context, endpoint string, cfg InitConfig) error

    // Probe readiness. Usually a GET /health with a short timeout loop.
    Ping(ctx context.Context, endpoint string) error
}

type InitConfig struct {
    AccessToken    string
    EnvVars        map[string]string
    DefaultUser    string   // "user"
    DefaultWorkdir string   // "/home/user"
    VolumeMounts   []VolumeMount
    HyperloopIP    string   // optional; stub for local use
}
```

### Phase 1 implementation: `internal/agent/upstream/`

The `UpstreamEnvdProvider` reuses the upstream E2B envd binary unchanged.

```go
type UpstreamEnvdProvider struct {
    Version   string   // e.g. "0.5.7"
    BinaryURL string   // override for GitHub releases URL
    CacheDir  string   // ~/.cache/edvabe/envd/<version>/
}
```

`EnsureImage` does:

1. Check if the target tag exists on the runtime. If yes → done.
2. Check the cache dir for `envd-<version>-linux-<arch>`. If missing:
   - Download from `https://github.com/e2b-dev/infra/releases/download/envd-v<version>/envd-linux-<arch>`
   - Verify sha256
   - `chmod +x`
   - Cache to disk
3. Materialize an embedded Dockerfile (`assets/Dockerfile.base`) plus the
   cached envd binary into a temp build context dir.
4. Call `runtime.BuildImage()` to produce the tagged image.

The embedded Dockerfile looks roughly like:

```dockerfile
FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-pip nodejs npm ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/*
RUN useradd -m -s /bin/bash user
COPY envd /usr/local/bin/envd
EXPOSE 49983
USER user
WORKDIR /home/user
CMD ["/usr/local/bin/envd", "--isnotfc"]
```

The `--isnotfc` flag is upstream envd's dev flag for running outside
Firecracker — see `e2b-infra/packages/envd/main.go:64-69`.

### Future implementation: `internal/agent/native/`

A native Go reimplementation of the envd wire protocol, compiled into a
separate static binary shipped alongside `edvabe` (or bundled via
`//go:embed`). Same `AgentProvider` interface. Phase 6+ work. Motivations
would be: faster debugging, pinned behavior regardless of upstream
changes, smaller image size, possibly running the agent as a subprocess
of edvabe rather than inside a container.

## Bootstrap flow

On `edvabe serve` first run:

```
1.  Load config from env vars / CLI flags.
2.  Initialize Runtime (Docker) — open socket, verify daemon.
3.  Initialize AgentProvider (UpstreamEnvd).
4.  Call agent.EnsureImage(runtime, "edvabe/base:latest").
       - if image missing on runtime → fetch envd binary →
         generate build context → runtime.BuildImage()
       - log progress to stderr: "building edvabe/base:latest (first run)"
5.  Start Sandbox Manager (in-mem map, watchdog goroutine).
6.  Start HTTP server on :3000.
7.  Log "ready" and sit in Accept loop.
```

Subsequent runs skip step 4's build because the image already exists.
Force rebuild via `edvabe build-image --force` or by deleting the tag.

## Request flow: `sandbox.create()`

```
1.  Client SDK
2.  → POST http://localhost:3000/sandboxes  (no sandbox header)
3.  → dispatch.go: no E2b-Sandbox-Id → Control router
4.  → control/sandboxes.go: decode NewSandbox
5.  → sandbox.Manager.Create(req)
         - mint sandboxID (isb_<ulid>) + envdAccessToken (ea_<random>)
         - resolve templateID → image tag ("base" → "edvabe/base:latest")
6.  → runtime.Docker.Create(CreateRequest{...})
         - ContainerCreate (image, env, labels, bind mounts)
         - ContainerStart
         - ContainerInspect → get bridge IP
         - return SandboxHandle{ContainerID, AgentHost, AgentPort: 49983}
7.  → agent.UpstreamEnvd.Ping(endpoint)   (poll /health until ready)
8.  → agent.UpstreamEnvd.InitAgent(endpoint, InitConfig{
           AccessToken: envdAccessToken,
           EnvVars, DefaultUser: "user", DefaultWorkdir: "/home/user",
       })
9.  → Manager registers *Sandbox in map[id]
10. → Manager spawns timeout watchdog goroutine
11. → Handler serializes *Sandbox → JSON:
         { sandboxID, envdVersion: "0.5.7", envdAccessToken,
           trafficAccessToken, domain: "localhost:3000", ... }
```

Target: under 1 second wall clock on a warm image.

## Request flow: `sandbox.commands.run("ls -la")`

This is where the envd passthrough shines — edvabe does almost nothing.

```
1.  Client SDK
2.  → POST http://localhost:3000/process.Process/Start
         Headers: E2b-Sandbox-Id=isb_..., X-Access-Token=ea_...
         Body: Connect-RPC frame {process: {cmd, args, envs, cwd}, pty, tag}
3.  → dispatch.go: E2b-Sandbox-Id present → Reverse Proxy
4.  → proxy.go:
         - look up sandbox by ID in Manager
         - fetch AgentEndpoint from Runtime: http://<bridge-ip>:49983
         - httputil.ReverseProxy.ServeHTTP rewrites URL,
           forwards request body unchanged,
           streams response body unchanged (including Connect-RPC
           server-stream frames — they just pass through as bytes)
5.  → envd inside container handles the RPC:
         - spawns /bin/bash -l -c "ls -la"
         - emits StartEvent{pid} → DataEvent{stdout}... → EndEvent{exitCode}
6.  → Client SDK parses the stream as usual
```

**edvabe does not parse Connect-RPC frames.** It is a dumb HTTP reverse
proxy for everything past `/process.Process/*` and `/filesystem.Filesystem/*`
and `/files` and `/init` and `/envs` and `/metrics` and `/health`. Same
for PTY, stream input, watchers — all free.

## Request flow: `sandbox.files.write("/home/user/foo.txt", "hello")`

```
1.  Client SDK
2.  → POST http://localhost:3000/files?path=/home/user/foo.txt
         Headers: E2b-Sandbox-Id=isb_..., X-Access-Token=ea_...
         Body: hello (octet-stream)
3.  → dispatch → Reverse Proxy
4.  → httputil.ReverseProxy → http://<bridge-ip>:49983/files?path=...
5.  → envd writes the file inside the container, returns [EntryInfo]
```

Again, no special handling. The proxy does not inspect the body.

## Sandbox state and persistence

v1 keeps everything in memory:

```go
type Sandbox struct {
    ID             string
    TemplateID     string
    ContainerID    string
    AgentHost      string    // resolved at Create time
    AgentPort      int       // usually 49983
    EnvdToken      string
    TrafficToken   string
    State          State     // running, paused
    Metadata       map[string]string
    EnvVars        map[string]string
    CreatedAt      time.Time
    ExpiresAt      time.Time
    Lifecycle      Lifecycle
    Network        NetworkConfig
    mu             sync.RWMutex
}

type Manager struct {
    sandboxes map[string]*Sandbox
    mu        sync.RWMutex
    runtime   runtime.Runtime
    agent     agent.AgentProvider
}
```

Pros: zero deps, fast startup, no migration concerns.

Cons: restart = lose the registry. The containers themselves survive an
edvabe restart; we just forget about them. A reconnect flow that rebuilds
state from `docker ps --filter label=edvabe.sandbox.id` can be added in
Phase 5 if wanted.

## Auth (or lack thereof)

`internal/api/auth.go` implements two middlewares:

- `requireAPIKey` — on control-plane routes, accepts any non-empty
  `X-API-Key`. 401 with E2B error shape if missing.
- The envd plane authenticates against envd itself — edvabe does **not**
  check `X-Access-Token`. It just forwards the header. envd verifies it
  against the token we told it about via `/init`.

## Sandbox container hardening (or lack thereof)

Sandbox containers are created with `seccomp=unconfined` and
`apparmor=unconfined` (see `internal/runtime/docker/create.go`). This
is a deliberate relaxation of Docker's default profile, and it matters
because:

- Modern tooling inside sandboxes — bun's install-script sandbox
  (`bwrap`), podman-in-sandbox, flatpak-builder, and similar — create
  new user namespaces. Docker's default seccomp profile and the
  host's AppArmor profile (Ubuntu 24.04+ ships
  `kernel.apparmor_restrict_unprivileged_userns=1`) both block that,
  so bwrap fails with `No permissions to create new namespace`.
- edvabe's threat model is single-user local dev (golden rule #5 in
  `CLAUDE.md`). The sandbox already runs arbitrary user code as root
  in a container on the user's laptop, so tightening seccomp buys
  little and breaks real tools.

`apparmor=unconfined` is silently ignored on hosts without AppArmor
(macOS Docker Desktop, Arch, Fedora), so the pair is portable.

If you ever move edvabe toward a multi-tenant or hosted posture, this
is one of the first lines to revisit.

## Routing by header

`internal/api/dispatch.go`:

```go
func Router(control, proxy http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        port := r.Header.Get("E2b-Sandbox-Port")
        id := r.Header.Get("E2b-Sandbox-Id")

        if id == "" {
            if p, sid, ok := parseHost(r.Host); ok {
                port, id = p, sid
                r.Header.Set("E2b-Sandbox-Id", id)
                r.Header.Set("E2b-Sandbox-Port", port)
            }
        }

        if id == "" {
            control.ServeHTTP(w, r)
            return
        }
        proxy.ServeHTTP(w, r)
    })
}
```

`parseHost` is a ~20-line copy of
`e2b-infra/packages/shared/pkg/proxy/host.go`.

The proxy handler looks at `E2b-Sandbox-Port`: `49983` → envd, `49999` →
code-interpreter overlay, anything else → user service (Phase 2). All
three go through the same `httputil.ReverseProxy` with a `Director` that
chooses the target URL based on the port.

## Package layout (proposed)

```
edvabe/
├── cmd/
│   └── edvabe/
│       └── main.go                 # serve, doctor, build-image, version
├── internal/
│   ├── api/
│   │   ├── control/                # control-plane REST only
│   │   │   ├── sandboxes.go        # POST /sandboxes, GET, DELETE, ...
│   │   │   ├── templates.go        # Phase 4
│   │   │   ├── stubs.go            # teams, api-keys, volumes, admin
│   │   │   └── router.go
│   │   ├── proxy.go                # reverse proxy for envd + CI + user ports
│   │   ├── dispatch.go             # header-based routing
│   │   ├── auth.go                 # X-API-Key middleware
│   │   └── errors.go               # E2B-compatible error envelope
│   ├── sandbox/
│   │   ├── manager.go              # lifecycle, in-mem registry
│   │   ├── sandbox.go              # struct, ID/token minting
│   │   ├── timeout.go              # TTL + refresh
│   │   └── metadata.go
│   ├── runtime/
│   │   ├── runtime.go              # interface + shared types
│   │   └── docker/
│   │       ├── runtime.go
│   │       ├── create.go
│   │       ├── pause.go
│   │       ├── commit.go
│   │       └── build.go
│   ├── agent/
│   │   ├── agent.go                # AgentProvider interface
│   │   └── upstream/               # upstream envd impl (Phase 1)
│   │       ├── provider.go
│   │       ├── fetch.go            # download binary from GitHub
│   │       └── image.go            # build image from embedded Dockerfile
│   ├── config/                     # env loading, socket discovery
│   ├── doctor/                     # edvabe doctor subcommand
│   └── telemetry/                  # slog + in-memory metrics
├── assets/                         # //go:embed
│   ├── Dockerfile.base             # edvabe/base image
│   └── Dockerfile.code-interpreter # edvabe/code-interpreter image (Phase 3)
├── docs/
├── go.mod
└── Makefile
```

Notice what's **gone** compared to earlier drafts:

- No `internal/proto/filesystem/` — we don't generate Connect-RPC stubs.
- No `internal/proto/process/` — same.
- No `internal/api/envd/` — envd is a passthrough.
- No `internal/api/ci/` — code interpreter is also a passthrough.
- No `internal/connect/` — no Connect-RPC framing code.
- No `internal/ndjson/` — NDJSON is passthrough.

The entire data plane is one `httputil.ReverseProxy`.

## Cross-cutting concerns

**Error handling**: every control-plane handler catches errors and emits
the E2B error envelope `{"code": <int>, "message": <str>}` with the
right HTTP status. Proxied requests pass through whatever envd returns.

**Logging**: structured JSON via `log/slog`. Each request gets a request
ID; proxied requests log both the incoming URL and the forwarded URL.

**Metrics**: per-endpoint counts/latencies in an in-memory ring buffer at
`/debug/metrics`.

**Shutdown**: SIGINT/SIGTERM handler stops accepting new requests, waits
for in-flight proxied streams (30 s grace), tears down running sandboxes
unless `--preserve-on-exit`, closes the listener.

**Doctor** (`edvabe doctor`):
- Docker socket reachable (tries `/var/run/docker.sock`, `~/.colima/docker.sock`,
  `~/.orbstack/run/docker.sock`, `~/.local/share/containers/podman/machine/podman.sock`)
- Docker version ≥ 20.10
- `edvabe/base:latest` image present (offer to build if missing)
- Port 3000 free
- envd binary cache status

## Single Go binary

Output: one statically-linked binary, embedded assets via `//go:embed`. No
separate agent binary shipped by edvabe itself — upstream envd comes
baked into E2B's `e2bdev/base` image, which edvabe pulls from Docker Hub
on first run (see [docs/07-open-questions.md#Q2](07-open-questions.md#Q2)).

```
$ ./edvabe serve            # start the HTTP server
$ ./edvabe doctor           # preflight check
$ ./edvabe build-image      # tag pulled e2bdev/base as edvabe/base:latest
$ ./edvabe pull-base        # pull upstream e2bdev/base (pre-warm cache)
$ ./edvabe version
```

User experience: `go install ./cmd/edvabe && edvabe serve` and everything
works. First run takes maybe 30-60 seconds to pull `e2bdev/base`
(~470 MB); subsequent runs are instant.
