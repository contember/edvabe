# 03 — API surface

The exact wire surface edvabe must implement for SDK compatibility, grouped
by priority tier. Each endpoint is annotated with where the SDKs call it and
what fields matter.

**Implementation note**: edvabe implements the *control plane* endpoints
itself (real Go handlers). The *envd* endpoints (filesystem, process,
`/files`, `/init`, `/health`, `/envs`, `/metrics`) and the *code
interpreter* overlay endpoints are **reverse-proxied** to the upstream
envd binary running inside the sandbox container. They are still part of
the surface edvabe is responsible for, but edvabe does not parse or
generate their bodies — it just forwards HTTP bytes. See
[05-architecture.md](05-architecture.md) for details.

## Priority tiers

- **T0 — minimum viable** — without this the SDK cannot create/run/kill a
  sandbox. Must ship in Phase 1.
- **T1 — core features** — filesystem + process APIs. Phase 2.
- **T2 — code interpreter overlay** — the Jupyter-like layer. Phase 3.
- **T3 — templates + pause/resume** — Phase 4.
- **T4 — stubs** — teams, quotas, volumes, admin. Return stable shapes so
  clients don't crash; no real behavior. Phase 5.

## Ports and base URLs

edvabe listens on a single HTTP port (default `:3000`). All three planes
(control, envd, code-interpreter) are served from the same process but
dispatched by URL path prefix or `Host` header:

```
control plane         : /  on the main port, matched when path is non-sandbox
envd REST + Connect   : /  when E2b-Sandbox-Id header is set (or Host has
                        the port-sandbox.localhost form)
code-interpreter      : /  when E2b-Sandbox-Port == 49999
```

To make the E2B SDKs resolve to edvabe, a client sets:

```sh
E2B_API_URL=http://localhost:3000
E2B_DOMAIN=localhost:3000
E2B_API_KEY=edvabe_local
E2B_SANDBOX_URL=http://localhost:3000
```

`E2B_SANDBOX_URL` is the load-bearing one for the data plane. Without
it, the SDK's default `get_sandbox_url` builds
`https://49983-<sandbox_id>.<E2B_DOMAIN>` — both HTTPS (which edvabe
does not terminate) and bound to a synthesized hostname. With the
override set, the SDK sends data-plane calls to plain HTTP `localhost`
and still emits the `E2b-Sandbox-Id` and `E2b-Sandbox-Port` headers
edvabe's reverse proxy dispatches on, so no wildcard DNS or TLS is
required.

Confirmed in the Python SDK (`e2b.connection_config.ConnectionConfig.
get_sandbox_url` short-circuits on `_sandbox_url`) and the TypeScript
SDK (`SandboxOpts.sandboxUrl` defaulting to `E2B_SANDBOX_URL`).

## Auth

edvabe accepts and ignores all credentials in v1:

- `X-API-Key: <anything>` on control plane routes
- `Authorization: Bearer <anything>` on admin/access-token routes
- `X-Access-Token: <anything>` on envd routes

It does enforce **shape** (header must exist) so misconfigured clients see a
clear 401. Real auth is intentionally off the table for a local dev tool.

---

## T0 — Minimum viable (Phase 1)

### Control plane

| Method | Path | Notes |
|---|---|---|
| `GET`    | `/health`                          | 204 No Content. |
| `POST`   | `/sandboxes`                       | Create. See request/response shapes below. |
| `GET`    | `/sandboxes/{sandboxID}`           | Returns `SandboxDetail`. 404 if gone. |
| `GET`    | `/v2/sandboxes`                    | Paginated list. `?state=running,paused&limit=&nextToken=`. Response uses `x-next-token` header. |
| `DELETE` | `/sandboxes/{sandboxID}`           | Kill. 204. |
| `POST`   | `/sandboxes/{sandboxID}/timeout`   | Body `{timeout: seconds}`. Reset TTL from now. 204. |
| `POST`   | `/sandboxes/{sandboxID}/connect`   | Reconnect/resume. Body `{timeout}`. 200 if already running, 201 if resumed from paused. |

**`POST /sandboxes` request body** (`NewSandbox`):

```json
{
  "templateID": "base",
  "timeout": 300,
  "autoPause": false,
  "autoResume": { "enabled": false },
  "secure": false,
  "allow_internet_access": true,
  "network": { "allowPublicTraffic": true, "allowOut": [], "denyOut": [] },
  "metadata": { "k": "v" },
  "envVars": { "KEY": "VAL" },
  "volumeMounts": []
}
```

**Response** (`Sandbox`):

```json
{
  "sandboxID": "isb_01HZXXXXXX",
  "templateID": "base",
  "clientID": "local",
  "envdVersion": "0.5.7",
  "envdAccessToken": "ea_...",
  "trafficAccessToken": "ta_...",
  "domain": "localhost:3000",
  "metadata": { "k": "v" },
  "startedAt": "2026-04-15T11:00:00Z",
  "endAt": "2026-04-15T11:05:00Z"
}
```

Fields the SDK reads: `sandboxID`, `envdVersion` (must be `>= 0.1.0`),
`envdAccessToken`, `domain`. Everything else is informational.

**`SandboxDetail`** (returned by `GET /sandboxes/{id}`) extends `Sandbox` with
`state` (`running`|`paused`), `cpuCount`, `memoryMB`, `diskSizeMB`,
`lifecycle: {onTimeout, autoResume}`, `network: SandboxNetworkConfig`,
`allowInternetAccess`, `envdAccessToken`.

### Envd REST

| Method | Path | Notes |
|---|---|---|
| `GET`  | `/health` | 204. Unauthenticated. SDK uses it for `is_running` probe. |
| `POST` | `/init`   | Orchestrator → envd handshake. edvabe calls this **on itself** during sandbox create. Accepts `{envVars, accessToken, timestamp, defaultUser, defaultWorkdir, volumeMounts}`. |
| `GET`  | `/envs`   | Current default env vars. |
| `GET`  | `/metrics`| Host metrics JSON: `{ts, cpu_count, cpu_used_pct, mem_total, mem_used, disk_total, disk_used}`. |
| `GET`  | `/files`  | Query `?path=&username=`. Raw `application/octet-stream` download. Accept-Encoding `gzip` honored. |
| `POST` | `/files`  | Query `?path=&username=`. Body either `multipart/form-data` with `file` part, or raw `application/octet-stream` (envd ≥ `0.5.7`). Response 200 with `[EntryInfo]`. |

### Envd Connect-RPC (process)

Service: `process.Process`, URL prefix `/process.Process/`.

| RPC | Kind | Request | Response |
|---|---|---|---|
| `List`       | unary         | `{}` | `{processes:[ProcessInfo]}` |
| `Start`      | server-stream | `StartRequest{process, pty?, tag?, stdin?}` | stream of `ProcessEvent` |
| `Connect`    | server-stream | `{process: ProcessSelector}` | stream of `ProcessEvent` |
| `SendInput`  | unary         | `{process, input: {stdin|pty: bytes}}` | `{}` |
| `SendSignal` | unary         | `{process, signal}` | `{}` |
| `CloseStdin` | unary         | `{process}` | `{}` |

`ProcessConfig` fields: `cmd`, `args[]`, `envs{}`, `cwd?`.
`ProcessSelector` is oneof `{pid: uint32, tag: string}`.
`StartRequest.pty` is `{size: {cols, rows}}`.

**Stream encoding**: Connect JSON server-stream. Each frame is a
length-prefixed JSON envelope `{event: {case, value}}`. Event cases:

- `start` → `{pid}` — emitted exactly once at the start
- `data`  → `{stdout|stderr|pty: base64-bytes}` — repeated
- `keepalive` → `{}` — emit periodically while idle
- `end`   → `{exitCode, exited, status?, error?}`

Order matters: `start` must come first, `end` must come last.

How the SDK encodes `sandbox.commands.run("ls -la")`
(`js-sdk/src/sandbox/commands/index.ts:425-434`):

```
ProcessConfig {
  cmd: "/bin/bash",
  args: ["-l", "-c", "ls -la"],
  envs: opts.envs,
  cwd: opts.cwd,
}
```

edvabe's implementation translates this to
`docker exec -i <container> /bin/bash -l -c "ls -la"` with stdout/stderr
piped back as `data` events.

### Envd Connect-RPC (filesystem)

Service: `filesystem.Filesystem`, URL prefix `/filesystem.Filesystem/`.

| RPC | Kind | Notes |
|---|---|---|
| `Stat`    | unary | `{path}` → `{entry: EntryInfo}` |
| `MakeDir` | unary | `{path}` → `{entry}` |
| `Move`    | unary | `{source, destination}` → `{entry}` |
| `ListDir` | unary | `{path, depth}` → `{entries:[EntryInfo]}` |
| `Remove`  | unary | `{path}` → `{}` |

`EntryInfo`: `{name, type: FILE|DIRECTORY, path, size: int64, mode: uint32, permissions: string, owner, group, modified_time: Timestamp, symlink_target?: string}`.

---

## T1 — Core features (Phase 2)

### Envd REST (additions)

No new endpoints — `POST /files` gets the octet-stream upload path flagged
for T0 already.

### Envd Connect-RPC (additions)

| RPC | Kind | Notes |
|---|---|---|
| `WatchDir`          | server-stream | `{path, recursive}` → stream of `{event: StartEvent|FilesystemEvent|KeepAlive}`. |
| `CreateWatcher`     | unary         | Polling alternative: returns `{watcher_id}`. |
| `GetWatcherEvents`  | unary         | `{watcher_id}` → `{events}`. |
| `RemoveWatcher`     | unary         | `{watcher_id}` → `{}`. |
| `Update` (process)  | unary         | Resize PTY: `{process, pty}` → `{}`. |
| `StreamInput` (proc)| client-stream | Bidi-style client stream for ordered stdin writes. |

PTY support on `Process.Start` — SDK invokes with `args: ["-i", "-l"]` and
injects `TERM=xterm-256color, LANG/LC_ALL=C.UTF-8`. On the wire, PTY output
comes back as `pty: bytes` (not split into stdout/stderr).

`FilesystemEvent.type` is one of `CREATE/WRITE/REMOVE/RENAME/CHMOD`.
`recursive: true` only works for envd ≥ 0.1.4 — we advertise 0.5.7 so it's
always available.

### Control-plane signed URLs

Envd supports pre-signed file download/upload URLs:
`v1_<sha256(path:op:user:token[:exp])>`. This lets agents hand a URL to an
unauthenticated third party. Can be punted to T3/T4 — the SDKs don't call it
for basic workflows.

---

## T2 — Code interpreter overlay (Phase 3)

When `E2b-Sandbox-Port == 49999`, edvabe routes to the code-interpreter
service inside that sandbox's container. Endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET`    | `/health`                  | 204. |
| `POST`   | `/execute`                 | Body `{code, context_id?, language?, env_vars?}`. Response: `application/x-ndjson` stream. |
| `POST`   | `/contexts`                | Body `{language, cwd?}` → `{id, language, cwd}`. |
| `GET`    | `/contexts`                | List. |
| `POST`   | `/contexts/{id}/restart`   | Restart kernel. |
| `DELETE` | `/contexts/{id}`           | Dispose. |

NDJSON event types (one per line):
`stdout`, `stderr`, `result`, `error`, `end_of_execution`,
`number_of_executions`, `unexpected_end_of_execution`.

`result` events carry base64 payloads per MIME type: text, html, markdown,
svg, png, jpeg, pdf, latex, json, javascript, plus any `chart_data_extractor`
output. The SDK's `Result` wrapper
(`code-interpreter/js/src/messaging.ts`) is the shape we must match.

Default languages in the upstream template: Python (context name `python`),
JavaScript (Deno-based, context `javascript`). Also R, Java, Ruby, Bash via
dynamic kernel creation.

Implementation option: ship a pre-built edvabe-compatible code-interpreter
image derived from `code-interpreter/template/build_docker.py` with the
systemd units replaced by direct process launches.

---

## T3 — Templates + pause/resume (Phase 4)

### Templates

| Method | Path | Notes |
|---|---|---|
| `GET`    | `/templates`                                 | List. Returns `[Template]`. |
| `GET`    | `/templates/{templateID}`                    | Returns `TemplateWithBuilds`. |
| `GET`    | `/templates/aliases/{alias}`                 | Returns `{templateID, public}`. |
| `POST`   | `/v3/templates`                              | Create (declarative metadata only). Body `{name?, tags[], cpuCount?, memoryMB?}`. |
| `POST`   | `/v2/templates/{templateID}/builds/{buildID}`| Start a build. Body `TemplateBuildStartV2 {fromImage?, fromTemplate?, steps:[{type, args, filesHash, force}], startCmd, readyCmd}`. |
| `GET`    | `/templates/{templateID}/builds/{buildID}/status` | Poll build status. |
| `GET`    | `/templates/{templateID}/builds/{buildID}/logs`   | Paginated structured logs. |
| `DELETE` | `/templates/{templateID}`                    | 204. |
| `PATCH`  | `/v2/templates/{templateID}`                 | `{public: bool}`. |

In edvabe a "template" is a Docker image tag. Build steps map onto a
generated Dockerfile that we `docker build`. Hash/cache by step `filesHash`.

### Pause / resume / snapshots

| Method | Path | Notes |
|---|---|---|
| `POST`   | `/sandboxes/{sandboxID}/pause`     | 204. Implementation: `docker pause`. |
| `POST`   | `/sandboxes/{sandboxID}/resume`    | 201 `Sandbox`. Deprecated — SDKs use `/connect`. |
| `POST`   | `/sandboxes/{sandboxID}/snapshots` | 201 `SnapshotInfo`. Body `{name?}`. Implementation: `docker commit` to an image tag. |
| `GET`    | `/snapshots`                       | List. |
| `POST`   | `/sandboxes/{sandboxID}/refreshes` | Legacy keepalive. Body `{duration}`. |

Pause semantics caveat: Docker pause freezes processes via SIGSTOP, not a
live memory snapshot. This is fine for "stop burning CPU while I edit" but
won't survive a binary restart. Snapshot (via `docker commit`) persists the
filesystem but not running memory — resume runs the template's start command
again.

### Network live-update

| Method | Path | Notes |
|---|---|---|
| `PUT` | `/sandboxes/{sandboxID}/network` | Body `{allowOut, denyOut}`. In edvabe v1 this is a stub that records state but does not enforce. |

### Metrics

| Method | Path | Notes |
|---|---|---|
| `GET` | `/sandboxes/{sandboxID}/metrics?start=&end=`     | Per-sandbox metrics window. |
| `GET` | `/sandboxes/metrics?sandbox_ids=a,b,c`           | Batch metrics for up to 100 IDs. |

Sourced from Docker stats or cgroup readouts.

### Logs

| Method | Path | Notes |
|---|---|---|
| `GET` | `/v2/sandboxes/{sandboxID}/logs?cursor=&limit=&direction=&level=&search=` | Paginated structured logs. |

Source: container stdout captured by edvabe into an in-memory ring buffer.

---

## T4 — Stubs (Phase 5)

Return stable shapes so clients don't crash. No real behavior.

### Teams / billing / quotas / API keys

| Method | Path | Stub behavior |
|---|---|---|
| `GET` | `/teams`                              | Single hard-coded "local" team. |
| `GET` | `/teams/{teamID}/metrics`              | Empty array. |
| `GET` | `/teams/{teamID}/metrics/max`          | Zero. |
| `GET/POST/PATCH/DELETE` | `/api-keys`            | In-memory CRUD on a fake table. |
| `POST/DELETE` | `/access-tokens`                 | In-memory CRUD. |

### Admin

`GET /nodes`, `POST /admin/...` — return 501 or empty. Real clients don't
call these.

### Volumes

| Method | Path | Stub behavior |
|---|---|---|
| `GET/POST/DELETE` | `/volumes`         | In-memory registry. |
| `GET` | `/volumes/{id}`                      | Returns `VolumeAndToken` with a fake JWT. |

Volume content gateway (`/volumecontent/...`) — implement as a thin wrapper
over a host directory if anyone asks. Bound a bind-mounted directory into
sandboxes at `/volumes/<name>`.

### MCP

`NewSandbox.mcp` — accept the field, log it, switch to a different default
template (`mcp-gateway`) or run `mcp-gateway --config '<json>'` as the start
command. Optional; can be punted.

---

## Deprecated endpoints (accept for compat)

- `POST /sandboxes/{id}/resume` — accept as alias for `/connect`.
- `POST /templates`, `POST /templates/{id}` — legacy v1 template build. Map to
  v3 handlers.
- `GET /sandboxes/{id}/logs` — legacy non-paginated logs.
- `clientID` and `alias/aliases` fields on response payloads — always
  present in responses for older SDK compatibility.

---

## Summary: endpoint count

| Tier | Control plane | Envd REST | Envd RPC | CI overlay | Total |
|---|---:|---:|---:|---:|---:|
| T0 | 7   | 5 | 9  | 0 | 21 |
| T1 | 0   | 0 | 6  | 0 |  6 |
| T2 | 0   | 0 | 0  | 6 |  6 |
| T3 | ~15 | 0 | 0  | 0 | 15 |
| T4 | ~10 | 0 | 0  | 0 | 10 |
| **Total v1** | **32** | **5** | **15** | **6** | **58** |

T0 alone — 21 endpoints — unblocks `Sandbox.create()` + `commands.run()` +
`files.read/write()`, which is the hot path for 80% of E2B SDK usage.
