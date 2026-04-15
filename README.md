# edvabe

Local [E2B](https://e2b.dev)-compatible sandbox runtime. A single Go
binary that exposes a wire-compatible subset of the E2B cloud sandbox
API on a developer's laptop: point an unmodified E2B SDK at it via env
vars and sandboxes run in local Docker containers instead of E2B's
cloud.

**Status**: v0.1.0 — Phase 1 acceptance met. `Sandbox.create`, commands,
filesystem, PTY, and directory watchers all work end-to-end against the
unmodified Python and TypeScript E2B SDKs. Code-interpreter overlay,
templates, and pause/resume are deferred to later phases — see
[docs/06-phases.md](docs/06-phases.md).

## Quick links

- **[CLAUDE.md](CLAUDE.md)** — project brief and working conventions
- **[docs/README.md](docs/README.md)** — documentation index
- **[docs/03-api-surface.md](docs/03-api-surface.md)** — wire surface
- **[docs/05-architecture.md](docs/05-architecture.md)** — architecture overview
- **[CHANGELOG.md](CHANGELOG.md)** — release notes

## Install

```sh
go install github.com/contember/edvabe/cmd/edvabe@latest
```

Or from a checkout:

```sh
make build           # produces ./bin/edvabe
./bin/edvabe version
```

## Run

```sh
edvabe doctor        # preflight check
edvabe build-image   # first-time: build edvabe/base:latest
edvabe serve         # listens on :3000 by default
```

`doctor` will tell you what's missing — Docker, base image, port. The
base-image build pins envd `0.5.7` and takes ~60 s on a fast connection
the first time; subsequent runs reuse Docker's layer cache.

## Point an SDK at it

Set these four env vars in the process running your E2B SDK code:

```sh
export E2B_API_URL=http://localhost:3000
export E2B_DOMAIN=localhost:3000
export E2B_API_KEY=edvabe_local
export E2B_SANDBOX_URL=http://localhost:3000
```

`E2B_SANDBOX_URL` is not optional. Without it the SDK tries to reach
envd at `https://49983-<sandbox_id>.<domain>` — both HTTPS (which
edvabe does not serve) and bound to a synthesized host form. With the
override set, the SDK routes data-plane calls to plain HTTP localhost
and still emits the `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers the
edvabe proxy dispatches on. See
[docs/03-api-surface.md](docs/03-api-surface.md#ports-and-base-urls)
for the details.

After that, unmodified SDK code works:

```python
from e2b import Sandbox

sbx = Sandbox.create(timeout=60)
result = sbx.commands.run("echo hello from edvabe")
print(result.stdout)  # "hello from edvabe\n"
sbx.kill()
```

## Test

```sh
make test            # go test ./...
make test-e2e-python # full hot-path against the Python SDK
make test-e2e-ts     # same against the TypeScript SDK
```

Both E2E suites spin up `edvabe serve`, run six tests covering create,
commands, filesystem read/write/list, PTY, and watchDir, then tear
down.

More commands in [CLAUDE.md](CLAUDE.md#commands).

## License

TBD.
