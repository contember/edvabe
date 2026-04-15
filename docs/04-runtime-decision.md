# 04 — Runtime decision: Docker (with a pluggable Runtime interface)

## Decision

**edvabe v1 uses Docker/OCI containers as the sandbox runtime, accessed via
the local Docker socket. A `Runtime` interface is designed from day one so
that a Firecracker or libkrun backend can be added later without rewriting
the HTTP layer. No Firecracker backend is built in v1.**

Note: the Runtime (where the sandbox runs) is orthogonal to the Agent
(what serves envd protocol inside the sandbox). Both are pluggable. In v1
we use Docker runtime + upstream envd agent. A future libkrun runtime
could still use the same upstream envd agent unchanged. A native Go agent
could plug into either runtime. See [05-architecture.md](05-architecture.md)
for the two interfaces.

## One-paragraph rationale

edvabe is a laptop dev tool, not a multi-tenant cloud. Firecracker is
Linux-with-KVM-only and unsupported on macOS/Apple Silicon, which is half of
the target userbase, and its template pipeline (Dockerfile → OCI image →
ext4 rootfs → provisioned microVM → snapshot) is months of work that
provides zero user value locally. Docker is already installed on every
target platform (Docker Desktop, Colima, OrbStack, Podman), speaks an
image format that *is* the E2B template format, and gives us exec/stdio/PTY,
port forwarding, filesystem, and resource limits with well-trodden Go
libraries. The security gap between a container and a microVM is real but
does not matter when the code being sandboxed is the user's own
LLM-generated code running on the user's own machine. The two things
Firecracker does uniquely well — sub-50 ms boots and live-memory snapshots —
are cloud concerns that can be approximated well enough locally with a warm
container pool and `docker commit`.

## Scoring summary (full analysis follows)

| Dimension | Docker | Firecracker | Winner |
|---|---|---|---|
| Host setup (Linux)                       | Works out of the box                  | Needs KVM, root, TAP, iptables              | Docker      |
| Host setup (macOS/Apple Silicon)         | Docker Desktop, Colima, OrbStack      | Unsupported upstream                        | Docker (big)|
| Host setup (Windows/WSL2)                | Docker Desktop WSL2                   | Nested virt, fragile                        | Docker      |
| API-level E2B compatibility              | ~95% (kernel modules, systemd are edge)| 100% on paper (you build the agent)        | Docker      |
| Cold start                               | 200–800 ms                            | ~125 ms cold, ~28 ms from snapshot          | Firecracker |
| Template format                          | Dockerfile = the template             | Dockerfile → OCI → ext4 → snapshot pipeline | Docker (big)|
| Filesystem ops                           | Bind mounts or sidecar agent          | In-VM agent required                        | Docker      |
| Process + PTY                            | `docker exec` + attach                | Custom vsock/serial agent                   | Docker      |
| Networking / port forward                | `-p host:guest`                       | TAP + iptables + NAT                        | Docker (big)|
| Snapshot/pause/resume                    | `docker pause` / `commit`              | Live UFFD-backed resume                     | Firecracker |
| Isolation strength                       | Namespaces + cgroups                  | KVM hardware boundary                       | Firecracker |
| Binary / runtime deps                    | Ships Go binary only                  | Ships firecracker + kernel + agent + builder| Docker      |
| Maintenance burden                       | ~3–6k lines of Go                     | ~15–25k lines + containerd/CNI deps         | Docker (big)|

**Docker wins 10 of 13 dimensions. The three losses (cold-start, snapshot,
isolation) are cloud concerns that do not apply to the product.**

## Detailed reasoning

### Host setup

The single blocking constraint: edvabe must work on macOS with Apple
Silicon. Firecracker requires `/dev/kvm` on Linux and is explicitly not
supported on macOS — the maintainers have closed macOS support tickets
stating it's out of scope. The only path to Firecracker on macOS is running
a Linux VM (Lima, Colima, etc.) and running Firecracker inside that, which
is exactly the nested-VM mess a "single binary on your laptop" tool is
trying to avoid.

Docker works on every target platform through Docker Desktop, Colima,
OrbStack, Rancher Desktop, or Podman. All of them expose a Unix socket that
edvabe's Docker client can talk to. There is no license lock-in on Docker
Desktop because the alternatives are drop-in.

Windows/WSL2: Docker Desktop's WSL2 backend is the well-worn path.
Firecracker inside WSL2 requires nested virtualization and is fragile.

This dimension alone is decisive. If we pick Firecracker, the tool does not
ship on half the target platforms.

### API-level compatibility

The E2B envd API exposes: read/write/watch files, spawn processes, stream
stdio, open PTYs, list processes, send signals, close stdin. Every one of
these maps cleanly onto a Docker primitive:

- **Read/write/list/stat/watch files** — `docker cp`, bind mounts, or a
  small in-container sidecar.
- **Spawn processes** — `docker exec` with the Go SDK's
  `ContainerExecAttach`, which supports PTYs and bidirectional streams.
- **Send signals / kill processes** — `docker kill` on the exec.
- **List processes** — `docker top` or a small sidecar.
- **Port forwarding** — `HostConfig.PortBindings` on container create.
- **Env vars / cwd / user** — exec options.

The failure modes, all of which are niche in E2B workloads:

- **Kernel modules / `modprobe`** — user code that tries to load kernel
  modules will fail in an unprivileged container. We document this and
  move on.
- **`systemd` as PID 1** — most E2B templates don't rely on systemd.
  The code-interpreter template does, but we can run jupyter + FastAPI as
  foreground processes instead of via systemd units.
- **Nested virtualization / `/dev/kvm`** — not exposed in a default
  container. Irrelevant for agent code.
- **A "real" `/proc`** — containers have `/proc`; differences are usually
  invisible unless workloads inspect cgroup files.

One incidentally nice property: E2B's own `fromDockerfile()` template
builder rejects multi-stage Dockerfiles, skips `EXPOSE`/`VOLUME`, and maps
`RUN`/`COPY`/`ENV` onto its own DSL. edvabe can accept raw Dockerfiles with
no restrictions — arguably *more* permissive than production E2B.

### Template format

This is almost as decisive as host setup. E2B templates are built by going
Dockerfile → Docker image → extracted rootfs → squashfs/ext4 → provisioned
microVM → snapshot. Reproducing that pipeline locally is not a weekend
project: it requires running Docker to build the image anyway (you have to
— nobody writes rootfs-builders for their own base images), extracting
layers, flattening, installing a `systemd`/`envd` binary, booting a VM
from the result, and taking a snapshot.

- **Docker backend**: `docker build -f Dockerfile -t edvabe/template:<id>`.
  The image *is* the template. Zero template-pipeline code.
- **Firecracker backend**: write or import the whole pipeline. Options are
  [firecracker-containerd](https://github.com/firecracker-microvm/firecracker-containerd)
  (heavy, GCE-oriented), the abandoned Weave Ignite, or a hand-rolled
  implementation. All are substantial.

Going Docker erases this category of work entirely.

### Filesystem

Three implementation options for the envd filesystem API on a Docker
backend, in order of simplicity:

1. **Bind-mount a per-sandbox host directory** (`/var/lib/edvabe/sb-<id>`) at
   `/home/user` inside the container. edvabe reads/writes files on the host
   side. Watches are `fsnotify` on the host. Fast to ship, breaks if user
   code touches files outside the bind mount.
2. **`docker cp` + `tar` streaming** for arbitrary in-container paths. No
   bind mount needed.
3. **Sidecar agent** — ship a small in-container Go binary that speaks the
   real envd Connect-RPC protocol, either baked into the template image or
   mounted via a bind mount. Few hundred lines; gives byte-exact
   compatibility. Option (3a) is to reuse upstream envd unmodified (it has
   an `isnotfc` dev flag for non-Firecracker execution).

Phase plan: start with (1)+(2), migrate to (3) in Phase 2 when compatibility
testing demands it.

### Process execution + streaming

`docker exec` with `Tty: true` plus `ContainerExecAttach` gives bidirectional
byte streams with real PTYs. Standard Go Docker SDK pattern used by every
AI-agent-shell product. PTY resize works via `ContainerExecResize`. Signals
work via `ContainerKill` (container-level) or exec-level equivalents.

The translation from Connect-RPC `Process.Start` to Docker is:

```
ProcessConfig{cmd, args, envs, cwd}
  → docker.ContainerExecCreate(containerID, ExecOptions{
      Cmd: append([]string{cmd}, args...),
      Env: envsToSlice(envs),
      WorkingDir: cwd,
      Tty: ptyRequested,
      AttachStdin: stdinKeepOpen,
      AttachStdout: true,
      AttachStderr: true,
    })
  → docker.ContainerExecAttach
  → for each chunk in attach.Reader: emit DataEvent
  → after wait: emit EndEvent{exitCode}
```

Keepalive events are emitted on a timer between data chunks.

### Networking and port forwarding

Docker: `HostConfig.PortBindings` is a map literal. `docker run -p` is a
flag. Done. Also gives you outbound internet, DNS, and a usable default
bridge network.

Firecracker: you create a TAP device per VM, attach it to a host bridge or
do NAT via iptables, manage an IP pool, assign MAC addresses, run DHCP or
pre-configure via kernel command line, and handle IP conflicts. Forwarding
ports is more iptables. This is several hundred lines of networking code
that needs root and whose failure modes look like "my laptop's WiFi broke."

For the per-sandbox URL scheme (`<port>-<id>.localhost`), edvabe runs a
reverse proxy on its main port that parses `E2b-Sandbox-Id` /
`E2b-Sandbox-Port` headers and forwards to the right container's bridge IP.
Wildcard DNS is unnecessary because `*.localhost` resolves to `127.0.0.1`
by RFC 6761 on every modern OS.

### Snapshot / pause / resume

This is Firecracker's real superpower, and the one place Docker genuinely
loses. A properly snapshotted microVM can restore in ~28 ms with full
memory state. E2B uses this to make "paused" sandboxes feel instant.

For a local dev tool, we do not need it:

- The user is running a handful of sandboxes, not a fleet.
- `docker pause` / `docker unpause` freezes processes via SIGSTOP and is
  fine for "stop burning CPU while I edit."
- `docker commit` + `docker start` approximates persistent snapshotting
  well enough for filesystem state (not memory). For most E2B workloads —
  stateless scripts, Jupyter kernels that can be re-instantiated — this is
  indistinguishable.
- The E2B API's pause/resume shape is just an ID-shaped handle, which we can
  implement against either primitive.

If the user's workload really needs live-memory pause/resume, that is a
signal they should run real E2B in the cloud and accept the network cost,
or an edge case that justifies building a Firecracker backend later.

### Isolation and security

In the cloud, Firecracker's per-sandbox kernel behind KVM means a container
escape is impossible. For edvabe this is not the threat model: the code
being sandboxed is the user's own LLM-generated code running on the user's
own laptop. If it escapes a container, it lands on the same machine that
already has the user's SSH keys and browser session. A microVM does not
protect the user from their own agent exfiltrating `~/.ssh/id_rsa` — only
network egress filtering does.

If a user ever needs microVM-grade isolation on their laptop, the right
product is a "run my whole agent under a VM" tool, not edvabe.

For Linux users who do want stronger isolation, gVisor (`runsc`) is a
drop-in runtime that gives you a user-space kernel without requiring KVM.
edvabe can expose this as a `--runtime=gvisor` flag without rewriting
anything — it is still a Docker runtime under the hood.

### Binary size and maintenance

- **Docker backend**: one statically-linked Go binary, ~10-20 MB. Speaks the
  Docker socket. Ships a `doctor` subcommand that detects socket paths at
  `/var/run/docker.sock`, `~/.colima/docker.sock`, `~/.orbstack/run/docker.sock`,
  `~/.local/share/containers/podman/machine/podman.sock` and tells the user
  what to install if none exist.
- **Firecracker backend**: ships or downloads the `firecracker` binary
  (Linux-only, not trivially cross-compilable), a guest kernel image
  (~10 MB), a rootfs builder (kaniko, buildkit, or custom), and an in-guest
  agent. Plus per-arch variants. Plus the code to wire all of it together.
  Plus root/capabilities setup.

One developer working nights can reach Docker-backend MVP in weeks. The
same developer reaching Firecracker-backend MVP is a quarterly project, and
it's the wrong quarterly project.

## The pluggable interface

edvabe defines a `Runtime` interface in `internal/runtime` with approximately
these methods (exact signatures to be refined in Phase 1 code review):

```go
type Runtime interface {
    // Sandbox lifecycle
    Create(ctx context.Context, req CreateRequest) (*Sandbox, error)
    Destroy(ctx context.Context, sandboxID string) error
    Pause(ctx context.Context, sandboxID string) error
    Resume(ctx context.Context, sandboxID string) (*Sandbox, error)
    Snapshot(ctx context.Context, sandboxID, name string) (*SnapshotInfo, error)

    // Data-plane primitives
    Exec(ctx context.Context, sandboxID string, req ExecRequest) (Exec, error)
    ReadFile(ctx context.Context, sandboxID, path string) (io.ReadCloser, error)
    WriteFile(ctx context.Context, sandboxID, path string, data io.Reader) error
    Stat(ctx context.Context, sandboxID, path string) (*FileInfo, error)
    ListDir(ctx context.Context, sandboxID, path string, depth int) ([]FileInfo, error)
    MakeDir(ctx context.Context, sandboxID, path string) error
    Move(ctx context.Context, sandboxID, src, dst string) error
    Remove(ctx context.Context, sandboxID, path string) error
    Watch(ctx context.Context, sandboxID, path string, recursive bool) (<-chan FSEvent, error)

    // Networking
    ForwardPort(ctx context.Context, sandboxID string, containerPort int) (hostPort int, err error)

    // Templates
    BuildTemplate(ctx context.Context, req BuildRequest) (*Template, error)
    ListTemplates(ctx context.Context) ([]Template, error)
    DeleteTemplate(ctx context.Context, templateID string) error
}

type Exec interface {
    Events() <-chan ProcessEvent
    Stdin() io.WriteCloser
    Resize(cols, rows uint16) error
    Signal(sig os.Signal) error
    Wait() (exitCode int, err error)
    Close() error
}
```

**Only the Docker implementation is written in v1.** No stub Firecracker.
No mock. When/if Firecracker is added, it becomes a second file
(`internal/runtime/firecracker/firecracker.go`) and a `--runtime=` flag. The
HTTP handler code in `internal/api/` does not change.

The cost of having the interface from day one is about one design session
and some up-front care to keep Docker-specific types out of the API layer.
The benefit is that the second backend is a day-one contributor's first PR
rather than a rewrite.

## What would change this decision

Firecracker (or libkrun, or Kata, or QEMU) becomes the right answer under
any of these conditions:

1. **The product pivots from laptop to self-hosted Linux workstation.** macOS
   concerns disappear and Firecracker's isolation becomes more appealing.
2. **Target users are all on KVM-enabled Linux.** A platform-eng tool can
   assume `/dev/kvm`; an app-dev tool cannot.
3. **Code from untrusted third parties gets executed.** If edvabe ends up
   in a trust-the-submitter product (CTF, shared demo, classroom), the
   isolation gap matters.
4. **Sub-50 ms create becomes a visible requirement** — e.g. per-test-case
   sandboxes in a tight CI loop. (More likely solved with a container pool
   than with Firecracker.)
5. **E2B's API evolves in a microVM-specific direction** — e.g. exposes
   kernel modules, `/dev/kvm`-in-sandbox, or `systemd-nspawn`-style
   primitives that containers cannot fake.
6. **libkrun matures into a cross-platform microVM with Apple Silicon
   support.** If that happens — microsandbox is early evidence it might —
   libkrun, not Firecracker, becomes the second backend. The pluggable
   interface pays off regardless.

None of those describe what we are building today, so: ship Docker.

## References

- [Firecracker on Apple Silicon — GitHub #5019](https://github.com/firecracker-microvm/firecracker/discussions/5019)
- [Firecracker network setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/network-setup.md)
- [E2B scaling Firecracker via OverlayFS](https://e2b.dev/blog/scaling-firecracker-using-overlayfs-to-save-disk-space)
- [E2B template base image docs](https://e2b.dev/docs/template/base-image)
- [E2B infra repo](https://github.com/e2b-dev/infra)
- [Firecracker vs QEMU — E2B blog](https://e2b.dev/blog/firecracker-vs-qemu)
- [microsandbox (libkrun, macOS Apple Silicon)](https://github.com/microsandbox/microsandbox)
- [Northflank: self-hostable alternatives to E2B](https://northflank.com/blog/self-hostable-alternatives-to-e2b-for-ai-agents)
- [Daytona — container-based, 71 ms creation](https://github.com/daytonaio/daytona)
- [Docker Sandboxes (microVM on macOS/Windows)](https://docs.docker.com/ai/sandboxes/)
- [OpenHands QEMU microVM backend proposal](https://github.com/OpenHands/OpenHands/issues/13203)
- [ForgeVM — 28 ms Firecracker snapshots](https://dev.to/adwitiya/how-i-built-sandboxes-that-boot-in-28ms-using-firecracker-snapshots-i0k)
- [firecracker-containerd](https://github.com/firecracker-microvm/firecracker-containerd)
- [Linux PTY + docker exec internals](https://iximiuz.com/en/posts/linux-pty-what-powers-docker-attach-functionality/)
- [go-dockerpty](https://github.com/fgrehm/go-dockerpty)
