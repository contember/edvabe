# 02 — E2B internals (reference)

Reference material compiled from reading `e2b-dev/e2b`, `e2b-dev/infra`, and
`e2b-dev/code-interpreter`. This is "how the real thing is built" — not a
design for edvabe. It exists so that when we make a local-friendly trade-off,
the reader can see what we are trading against.

File paths cited here are relative to the upstream repos as of April 2026.

## Two-plane architecture

E2B exposes **two completely separate HTTP surfaces**:

1. **Control plane** — classic REST at `https://api.<domain>`
   (default `api.e2b.app`). Creates/lists/pauses/kills sandboxes, manages
   templates and teams. Spec: `infra/spec/openapi.yml`.
2. **Data plane (envd)** — served *inside every running sandbox* on TCP port
   **49983**, exposed to the outside via a per-sandbox subdomain. A mix of
   Connect-RPC (JSON) streaming methods and a small REST surface for
   `/health`, `/metrics`, `/init`, `/files`, `/envs`.
   Specs: `e2b/spec/envd/envd.yaml` plus two `.proto` files under
   `e2b/spec/envd/{filesystem,process}/`.

A third optional plane exists on port **49999** inside sandboxes built from
the `code-interpreter-v1` template: a FastAPI + Jupyter overlay that exposes
an `/execute` endpoint returning newline-delimited JSON. Its source lives in
`code-interpreter/template/server/`.

## Sandbox URL scheme

This is the single most important fact about how clients reach a sandbox:

```
https://<port>-<sandboxID>.<domain>
```

The leftmost subdomain is parsed by splitting on `-`. Where `<domain>` is the
value of `E2B_DOMAIN` (default `e2b.app`), `<sandboxID>` is returned by
`POST /sandboxes`, and `<port>` is the TCP port to reach inside the sandbox:
`49983` for envd, `49999` for the code-interpreter layer, any user port for
user HTTP servers.

The JS SDK always sends headers `E2b-Sandbox-Id` and `E2b-Sandbox-Port`
alongside the host, so server-side routing can use headers instead of DNS.
This is the trick edvabe exploits to avoid wildcard DNS locally.

Debug mode (`E2B_DEBUG=true`) short-circuits to
`http://localhost:3000` for control plane and `http://localhost:49983` for
envd, no subdomains required.

Source: `e2b/packages/js-sdk/src/connectionConfig.ts:86-151`,
`e2b/packages/python-sdk/e2b/connection_config.py:145-159`.

## Control plane

Go service, Gin HTTP router, port 80. Main handlers under
`infra/packages/api/internal/handlers/`. Authenticates against Supabase for
user tokens and a Postgres-backed API-key table for team tokens.

When a `POST /sandboxes` arrives, the handler picks an orchestrator node via
placement logic in `packages/api/internal/orchestrator/placement/`, calls the
orchestrator's gRPC `CreateSandbox`, waits for readiness, and returns the
sandbox record including `envdAccessToken` (a freshly-minted bearer that the
client will use against envd).

Sandbox state cache lives in Redis. The `sandboxes-catalog` package is read
by the client-proxy for routing.

## Orchestrator

Per-node Go daemon. Lives at `infra/packages/orchestrator/`. Exposes gRPC
(proto in `orchestrator.proto`) plus an HTTP reverse proxy on port 5007 for
data-plane traffic.

Responsible for:

- Spawning the Firecracker process per sandbox
  (`pkg/sandbox/fc/process.go:174-181` — `exec.Command("firecracker", ...)`
  wrapped in `unshare -m`).
- Assigning a network slot (`pkg/sandbox/network/slot.go`): one host IP out of
  `10.11.0.0/16`, a veth pair out of `10.12.0.0/16`, a TAP device inside a
  per-sandbox Linux network namespace.
- Managing lazy rootfs via NBD + overlay (`pkg/sandbox/rootfs/nbd.go`).
- Resuming from snapshot via userfaultfd for lazy memory fetch
  (`pkg/sandbox/uffd/uffd.go`).
- Placing each Firecracker process into a cgroup v2 under
  `/sys/fs/cgroup/e2b/` via `CLONE_INTO_CGROUP`.
- Routing in-bound data-plane traffic to the right VM via the orchestrator
  proxy on `:5007`, which looks up `sandbox.ID → slot.HostIP` in an
  in-memory map.

Template build system is a separate subsystem under
`orchestrator/pkg/template/build/`. It converts an OCI image into a
bootable Firecracker rootfs through a pipeline: pull image →
`filesystem.Make()` ext4 → overlayfs unpack → rsync → shrink → boot a VM and
run `provision.sh` (installs `systemd systemd-sysv openssh-server sudo chrony
socat curl ca-certificates fuse3 iptables git nfs-common`, symlinks
`/lib/systemd/systemd → /usr/sbin/init`) → snapshot. Each step is a layer;
the snapshot *is* the template artifact. Storage is diff-chunked in GCS.

## envd (the in-VM agent)

A Go binary at `infra/packages/envd/main.go`. Runs inside every sandbox on
port 49983. Baked into the rootfs during template build
(`HOST_ENVD_PATH=../envd/bin/envd`).

Architecture:

- **go-chi** HTTP router plus **Connect-RPC** (`connectrpc.com/connect`) on a
  single HTTP/1.1 listener.
- Two Connect services: `filesystem.Filesystem` and `process.Process`.
  Generated descriptors live in
  `e2b/packages/js-sdk/src/envd/{filesystem,process}/*_connect.ts`.
- REST routes: `GET /health`, `GET /metrics`, `POST /init`, `GET /envs`,
  `GET /files`, `POST /files`.
- **MMDS polling** at startup to fetch per-sandbox config (sandbox ID, team
  ID, logs collector address) from Firecracker's metadata service
  (`main.go:164-169`).
- **Port scanner** (`internal/port/forward.go`) that watches for TCP listeners
  on `127.0.0.1`/`localhost`/`::1` and spawns `socat` processes to re-bind
  each one on `169.254.0.21:<port>` (the in-VM gateway IP) so the host-side
  proxy can reach them.
- **cgroups v2** with three subgroups: `ptys` (high CPU weight), `socats`
  (background, light), `user` (capped just under host total).
- **Auth middleware** (`internal/api/auth.go:26-53`) that requires
  `X-Access-Token` on all Connect-RPC routes and on `/envs`. Exempts
  `GET /health`, `GET/POST /files` (which use signed URLs or separate auth),
  and `POST /init` (which authenticates via the orchestrator's handshake).
- **Sandbox-user selection** via HTTP Basic-Auth where the username is the
  `os/user` name and the password is empty
  (`js-sdk/src/envd/rpc.ts:60-93`).

An `isnotfc` dev flag exists at `envd/main.go:64-69` that lets envd run in a
plain Docker container for local dev — which means we can compile envd
unmodified and use it as the in-container agent if we want full on-wire
compatibility.

## Connect-RPC streaming format

Process.Start and Process.Connect are **server-stream** RPCs that emit a
`ProcessEvent` oneof message per frame:

```
StartEvent   { pid }                        // exactly once at the start
DataEvent    { stdout|stderr|pty: bytes }    // repeated
KeepAlive    {}                              // periodically while idle
EndEvent     { exit_code, exited, status, error? }
```

Encoding is Connect's JSON server-stream format: each frame is a
length-prefixed JSON object containing `{event: {...}}`. The SDK creates its
transport via `createConnectTransport({ useBinaryFormat: false })`
(`js-sdk/src/sandbox/index.ts:153-183`) and the Python SDK uses
`e2b_connect` with `json=True`, so **JSON is the wire format the SDK
actually uses** in practice. Binary protobuf is defined but not used.

Long-lived streams inject a `Keepalive-Ping-Interval` request header (SDK
default 50 seconds); envd emits `KeepAlive` events at that interval so
intermediate proxies don't time the connection out. Downstream HTTP idle
timeout in envd is 640 seconds, max-age 2 hours.

Filesystem.WatchDir is also a server-stream RPC; there is an alternative
polling trio (`CreateWatcher` / `GetWatcherEvents` / `RemoveWatcher`) for edge
runtimes that can't keep a long HTTP connection open.

## Client-proxy

Lives at `infra/packages/client-proxy/`. Edge proxy in front of every
orchestrator node. Parses the leftmost subdomain with
`strings.Split(host, "-")`, takes the first chunk as port and second as
sandbox ID (`packages/shared/pkg/proxy/host.go:41-66`). Alternatively accepts
`E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers if present. Looks up the
sandbox in the Redis catalog to find the orchestrator node IP, then proxies
to `orchestrator:5007` on that node. The orchestrator proxy in turn looks up
`sandbox.ID → slot.HostIP` in its in-memory map and proxies into the VM's
network namespace.

End-to-end path for a "run this command" request:

```
client SDK
  → https://49983-<sandboxID>.e2b.app
  → client-proxy (parse Host, Redis lookup → orchestrator node IP)
  → orchestrator:5007 (parse sandbox ID → slot.HostIP)
  → iptables DNAT into the VM's netns
  → tap0 → VM eth0:49983
  → envd process listening on 49983
```

## Code interpreter

Lives at `e2b-dev/code-interpreter`. It is a **template**, not a core API —
any sandbox built from `code-interpreter-v1` (`template/`) runs a FastAPI
server on 49999 that proxies to a local Jupyter kernel gateway at
`localhost:8888`.

Endpoints exposed by the FastAPI wrapper (`template/server/main.py`):

| Method | Path | Purpose |
|---|---|---|
| `GET`    | `/health`                      | Liveness |
| `POST`   | `/execute`                     | Run code. Body `{code, context_id?, language?, env_vars?}`. Streams NDJSON. |
| `POST`   | `/contexts`                    | Create kernel. Body `{language, cwd?}` → `{id, language, cwd}`. |
| `GET`    | `/contexts`                    | List. |
| `POST`   | `/contexts/{id}/restart`       | Restart kernel. |
| `DELETE` | `/contexts/{id}`               | Dispose. |

Output event types (one per NDJSON line): `stdout`, `stderr`, `result`,
`error`, `end_of_execution`, `number_of_executions`,
`unexpected_end_of_execution`. Rich outputs carry base64 payloads per MIME
type (text, html, markdown, svg, png, jpeg, pdf, latex, json, javascript,
plus chart-data extracted by the `chart_data_extractor` package).

Inside a sandbox the code interpreter is two systemd units: `jupyter.service`
and `code-interpreter.service`. For edvabe we can reuse the upstream
Dockerfile that `template/build_docker.py` and `build_prod.py` produce and
skip the systemd wrapper — running jupyter and FastAPI as foreground
processes or under `supervisord` inside a Docker container works fine.

## What "pause/resume" actually is

Firecracker snapshots plus userfaultfd. When a sandbox is paused the
orchestrator tells Firecracker to take a snapshot, diff-uploads the memfile
and rootfs deltas to storage, and frees the VM. On resume it starts a new
Firecracker, hands it the snapshot, and wires a UFFD socket so page faults
are served from chunked storage on demand. This is how E2B achieves the
~28 ms resume they advertise — memory is lazy-loaded, not pre-copied.

None of that is reproducible in Docker. See
[04-runtime-decision.md](04-runtime-decision.md) for the consolation prizes.

## Networking to the outside world

Each sandbox's netns has iptables NAT rules that SNAT outbound traffic to the
slot's host IP and DNAT inbound traffic back. Egress policy
(`allowOut`/`denyOut`) is enforced by nftables on the host, not inside envd.
An "egress proxy" runs in each orchestrator node to filter domain-based
rules.

For edvabe, Docker's default bridge network and `--network=host` (Linux only)
give you outbound access for free, and container port publishing gives you
inbound.

## Versioning notes

The SDK branches on `envdVersion` (returned in the `Sandbox` response) to
decide which newer RPCs are available:

- `0.1.0` — minimum; older is rejected outright
  (`sandboxApi.ts:814-820`).
- `0.1.4` — recursive `WatchDir`.
- `0.3.0` — `stdin: false` option on `Start`.
- `0.4.0` — default-user selection without explicit Basic auth.
- `0.5.2` — `CloseStdin` RPC.
- `0.5.7` — raw `application/octet-stream` uploads on `POST /files`.
- `99.99.99` — `ENVD_DEBUG_FALLBACK` sentinel used in debug mode.

**edvabe will advertise `envdVersion = "0.5.7"`** so the SDK takes the
newest code path in every branch and we don't have to implement legacy
fallbacks.

## Summary of what's reusable vs what's not

**Directly reusable** from the upstream repos:
- `envd` binary (baked into a Docker image). This gives us byte-exact
  compatibility with the SDKs without reimplementing the Connect-RPC layer.
- The code-interpreter Docker image and its FastAPI server.
- The OpenAPI control-plane spec (`openapi.yml`) as source of truth for
  request/response shapes.
- The `.proto` files for filesystem/process services — regenerated in Go to
  build our own envd later.

**Not reusable** — cloud-infra concerns we strip out:
- The API service, client-proxy, orchestrator gRPC plumbing, Redis catalog,
  Nomad job specs, placement logic.
- Network slot allocation, veth/TAP creation, iptables rules, egress proxy.
- NBD-backed lazy rootfs, chunked diff storage, UFFD resume.
- Template build pipeline (OCI → ext4 → provision.sh → snapshot).
- Supabase auth, access-token minting, team management, quotas.
- Grafana/Loki/Tempo/Mimir/Clickhouse observability stack.
