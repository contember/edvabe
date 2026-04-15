# edvabe

Local [E2B](https://e2b.dev)-compatible sandbox runtime. A single Go
binary that exposes a wire-compatible subset of the E2B cloud sandbox
API on a developer's laptop: point an unmodified E2B SDK at it via env
vars and sandboxes run in local Docker containers instead of E2B's
cloud.

**Status**: Phase 1 (early development, skeleton only — no working
functionality yet).

## Quick links

- **[CLAUDE.md](CLAUDE.md)** — project brief and working conventions
- **[docs/README.md](docs/README.md)** — documentation index
- **[docs/08-phase1-checklist.md](docs/08-phase1-checklist.md)** — concrete Phase 1 task list
- **[docs/05-architecture.md](docs/05-architecture.md)** — architecture overview

## Build

```sh
make build
./bin/edvabe version
```

More commands in [CLAUDE.md](CLAUDE.md#commands).

## License

TBD.
