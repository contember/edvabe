# edvabe

Local [E2B](https://e2b.dev)-compatible sandbox runtime. A single Go
binary that exposes a wire-compatible subset of the E2B cloud sandbox
API on a developer's laptop: point an unmodified E2B SDK at it via env
vars and sandboxes run in local Docker containers instead of E2B's
cloud.

## Features

- **Drop-in compatible** with unmodified E2B Python and TypeScript SDKs
- **Single binary** — no Redis, Postgres, Nomad, or Firecracker
- **Cross-platform** — Linux and macOS (x86_64 + arm64)
- **Sandbox lifecycle** — create, kill, pause, resume, snapshots
- **Custom templates** — programmatic `Template.build()` from the SDK
- **Code interpreter** — `@e2b/code-interpreter` SDK works out of the box
- **Full API surface** — teams, volumes, api-keys stubs so the SDK never crashes

## Quick start

### Option A: Go install

```sh
go install github.com/contember/edvabe/cmd/edvabe@latest

edvabe doctor        # preflight check
edvabe build-image   # first-time: build edvabe/base:latest (~60s)
edvabe serve         # listens on :3000
```

### Option B: Docker (recommended for docker-compose setups)

```sh
docker run --rm \
  -p 3000:3000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v edvabe-data:/data \
  ghcr.io/contember/edvabe:main serve
```

### Docker Compose

```yaml
services:
  edvabe:
    image: ghcr.io/contember/edvabe:main
    ports:
      - "3000:3000"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - edvabe-data:/data
    environment:
      EDVABE_STATE_DIR: /data
      EDVABE_CACHE_DIR: /data/cache

volumes:
  edvabe-data:
```

The Docker socket mount is required — edvabe creates sandbox containers
as siblings on the host Docker daemon.

## Point an SDK at it

Set these env vars in the process running your E2B SDK code:

```sh
export E2B_API_URL=http://localhost:3000
export E2B_DOMAIN=localhost:3000
export E2B_API_KEY=edvabe_local
export E2B_SANDBOX_URL=http://localhost:3000
```

When running inside docker-compose, replace `localhost:3000` with
`edvabe:3000` (the service name).

Then unmodified SDK code works:

```python
from e2b import Sandbox

sbx = Sandbox.create(timeout=60)
result = sbx.commands.run("echo hello from edvabe")
print(result.stdout)  # "hello from edvabe\n"
sbx.kill()
```

### Code interpreter

```sh
edvabe build-image --template=code-interpreter   # ~10 min first time
```

```python
from e2b_code_interpreter import Sandbox

sbx = Sandbox()
execution = sbx.run_code("1 + 1")
print(execution.results[0].text)  # "2"
sbx.kill()
```

### Custom templates

The SDK's programmatic `Template.build()` works against edvabe — it
translates step arrays into Dockerfiles and builds them locally:

```typescript
import { Template, Sandbox } from 'e2b'

const tpl = Template()
  .fromImage('oven/bun:slim')
  .aptInstall(['curl', 'git'])
  .runCmd('echo "built" > /etc/marker')

await Template.build(tpl, { alias: 'my-template' })
const sbx = await Sandbox.create('my-template')
```

## Commands

```sh
edvabe serve [--port 3000]                          # start server
edvabe doctor [--port 3000]                         # preflight checks
edvabe build-image [--template=base|code-interpreter|all]  # build images
edvabe pull-base                                    # pull upstream e2bdev/base
edvabe version                                      # print version
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `EDVABE_STATE_DIR` | `~/.local/share/edvabe` | Template store (templates.json) |
| `EDVABE_CACHE_DIR` | `~/.cache/edvabe/template-files` | File context cache |
| `EDVABE_BUILD_DIR` | `~/.cache/edvabe/builds` | Build scratch directory |
| `DOCKER_HOST` | auto-detected | Docker socket path |

## Test

```sh
make test                             # go test ./...
make test-e2e-python                  # Python SDK E2E (create, commands, files, pty, watch)
make test-e2e-ts                      # TypeScript SDK E2E
make test-e2e-code-interpreter-python # code interpreter Python E2E
make test-e2e-code-interpreter-ts     # code interpreter TypeScript E2E
```

## Architecture

edvabe implements the E2B control plane in Go. The data plane
(filesystem, process, PTY, watchers) is handled by upstream `envd`
running inside each sandbox container and reverse-proxied by edvabe.
This keeps edvabe small (~3000 LOC) while providing byte-exact wire
compatibility with the SDK.

```
SDK  ──HTTP──>  edvabe (:3000)
                  │
                  ├── control plane (Go handlers)
                  │     POST /sandboxes, GET /templates, ...
                  │
                  └── reverse proxy ──> container (envd :49983)
                                        filesystem, process, PTY, watch
                                        code-interpreter (:49999)
```

See [docs/05-architecture.md](docs/05-architecture.md) for details.

## Documentation

- [CLAUDE.md](CLAUDE.md) — project brief and conventions
- [docs/03-api-surface.md](docs/03-api-surface.md) — wire surface
- [docs/05-architecture.md](docs/05-architecture.md) — architecture
- [docs/06-phases.md](docs/06-phases.md) — delivery phases
- [CHANGELOG.md](CHANGELOG.md) — release notes

## License

TBD.
