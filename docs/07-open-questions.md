# 07 — Open questions

Design questions to decide during Phase 1 implementation. Each has a
current leaning but is not yet locked in.

## Q1: Reuse upstream envd, or write our own? — *resolved*

**Decided**: Phase 1 reuses the upstream E2B `envd` binary unchanged, baked
into `edvabe/base:latest`. A native Go reimplementation is Phase 5
(optional), built only if upstream envd causes friction.

**Why**: envd speaks exactly the Connect-RPC protocol the SDKs expect, has
an `--isnotfc` dev flag for running in plain Docker (see
`e2b-infra/packages/envd/main.go:64-69`), is Apache-2.0, and saves
~500-1000 lines of Go plus an ongoing sync burden with upstream. The
`AgentProvider` interface hides envd behind an abstraction so swap-out
later is a contained change.

**Risk**: envd's behavior outside Firecracker may have quirks (MMDS
polling, cgroups setup, port forwarder via socat) that work differently
or fail loudly inside a container. Validate during Phase 1 kickoff.

## Q2: How to distribute the envd binary? — *resolved*

**Decided**: Phase 1 uses option (D'), reframed: pull E2B's own
multi-arch `e2bdev/base` image from Docker Hub, pinned by OCI image
index digest, and tag it as `edvabe/base:latest`. We do not ship a
separate envd artifact and we do not maintain our own base image.

**Why.** Investigation on 2026-04-15 showed:

- (A) is impossible — `e2b-dev/infra` GitHub releases carry zero assets.
  envd is uploaded to E2B's private GCP/AWS buckets (see
  `packages/envd/Makefile` upload target), not to GitHub.
- (B) is out per the original Q2 write-up — Go toolchain on the host is
  too heavy a dependency for a `go install` UX.
- (C) would still need a source for the binary, inheriting (A)'s problem.
- (D) as originally framed meant edvabe publishing its own image. But
  E2B already publishes `e2bdev/base` multi-arch (amd64 + arm64), ~470 MB
  per arch, last-pushed dates track their releases. We can consume it
  directly.

Task 4 now pins `docker.io/e2bdev/base@sha256:<digest>` in code; task 5
tags that image as `edvabe/base:latest`. No `fetch-envd` subcommand, no
host-side binary cache.

**Pin version.** Bump by HEAD-requesting `e2bdev/base:latest` and
updating `BaseImageDigest`. Never track `latest` at runtime. The
`envdVersion` reported in Sandbox responses (CLAUDE.md golden rule #3)
remains `"0.5.7"` regardless — it's informational for the SDK, not tied
to the image pin.

## Q3: Does upstream envd actually run cleanly in a plain Docker container?

**The question.** envd has `--isnotfc` but we have not verified the
behavior end-to-end. Known concerns:

- **MMDS polling** — envd tries to read sandbox config from Firecracker's
  metadata service. In Docker mode this should fail silently and fall
  back, but we need to confirm it does.
- **Port forwarder via socat** — envd spawns `socat` processes to relay
  user-bound ports to the in-VM gateway IP `169.254.0.21`. In Docker that
  IP does not exist. This may need a replacement or disabling.
- **cgroups v2 setup** — envd creates per-process subgroups (ptys,
  socats, user). May need `--privileged` or may fail gracefully.
- **Systemd time setting** — the `/init` handler can adjust system clock
  via `clock_settime`. Needs `CAP_SYS_TIME` or should be disabled.

**Current leaning.** Run `docker run --rm edvabe/base:latest` locally in
Phase 1 kickoff as a smoke test. Document required capabilities or bind
mounts. Possibly patch upstream envd with a "truly Docker" mode flag if
needed and upstream it.

## Q4: `E2b-Sandbox-Id` header vs subdomain parsing

**The question.** The JS SDK sends `E2b-Sandbox-Id` / `E2b-Sandbox-Port`
headers with every data-plane call. Python SDK behavior should be
symmetric but unverified. If Python does not, edvabe needs to parse
`Host: <port>-<id>.<rest>`.

**Current leaning.** Implement both: check headers first, fall back to
host parsing. Cost is ~30 lines and covers every SDK version.

**Confirm during Phase 1.** Intercept one request from each SDK to verify.

## Q5: Timeout semantics for paused sandboxes

**The question.** E2B sandbox timeouts fire `onTimeout: "kill"` or
`"pause"`. With `pause`, does the sandbox eat resources forever if nobody
reconnects?

**Current leaning.** Paused sandboxes expire after a longer TTL
(configurable, default 24 hours) and are then force-killed. Logged.
`--paused-ttl=0` means live forever until edvabe binary restart (single-
user local dev).

## Q6: How do we match sandbox ID / token prefixes?

**The question.** E2B uses `isb_` for sandbox IDs, `ea_` for envd access
tokens, `ta_` for traffic tokens. Do we match?

**Current leaning.** Yes — cheap insurance against clients that parse
prefixes. `isb_` + base32 for IDs, `ea_` / `ta_` + base64url for tokens.
If E2B changes their format, we don't have to follow.

## Q7: How does edvabe bootstrap on first run?

**The question.** User runs `go install` and then `edvabe serve`. What
happens on the first invocation?

**Current leaning.**

1. `edvabe serve` runs the equivalent of `edvabe doctor` first.
2. Doctor detects: Docker socket reachable; `edvabe/base:latest` missing.
3. Doctor asks (y/n) to build the image, unless `--auto-build` is set.
4. Build: fetch envd binary → materialize build context → `docker build`.
5. Once image exists, start HTTP server.

Total first-run time: ~30-60 seconds on a fast connection.

**Alternative.** `edvabe serve` fails if the image is missing and prints
"run `edvabe build-image` first." Less magical, more explicit. Pick one
based on UX preference; v1 can start with the magical version.

## Q8: Concurrency limits

**The question.** How many sandboxes can edvabe manage simultaneously?
E2B cloud has thousands per node; a laptop has maybe 10-50.

**Current leaning.** Soft cap 50, configurable. Hard cap at whatever
Docker allows. Exceeding soft cap emits a warning and succeeds anyway;
hard cap returns 429.

## Q9: Streaming backpressure

**The question.** Because edvabe is a reverse proxy, backpressure is
mostly handled by `httputil.ReverseProxy` and Go's HTTP/1.1 streaming
semantics. But: a runaway process (`yes | head -c 10G`) inside a sandbox
pushes data to envd → to edvabe → to the client. If the client is slow,
where does it buffer?

**Current leaning.** Rely on Go's standard proxy flow-control — the client
read speed propagates back to the envd connection via TCP backpressure. No
buffering in edvabe beyond what the proxy already does. Prototype a stress
test in Phase 1 to confirm.

## Q10: Multi-user edvabe

**The question.** Can two developers share one `edvabe serve` on a shared
laptop/dev box?

**Current leaning.** v1 is single-user. Multi-user = out of scope.

## Q11: `envdVersion` pinning

**Decided** (from docs/03): `"0.5.7"`. Revisit if a phase depends on
functionality only in a newer envd.

## Q12: Docker SDK vs os/exec

**Decided**: use `github.com/docker/docker/client` (the official Go SDK).
Handles hijack/exec streaming correctly, is what every Go-based Docker
tool uses, supports container lifecycle + exec + events + stats + build.
Vendoring adds ~10 MB — fine for a dev tool.

## Q13: Where do sandboxes' bind mounts live on the host?

**The question.** If we want to provide a user-accessible host directory
for a sandbox's `/home/user` (so the user can `open` the files on their
laptop), where does it live?

**Current leaning.** `~/.local/share/edvabe/sandboxes/<id>/fs/` on Linux,
`~/Library/Application Support/edvabe/sandboxes/<id>/fs/` on macOS. Logged
at sandbox create time. Cleaned up on destroy unless `--preserve-fs` is
set.

**Caveat.** With the envd passthrough approach this is *optional* — envd
can read/write inside the container without any bind mount. The bind
mount is purely a convenience for the user to see files on their host.

## Q14: What happens when upstream envd is updated?

**The question.** We pin a version. Users may want to bump it. What's the
upgrade flow?

**Current leaning.**
- edvabe maintainer bumps `upstream.BaseImageDigest` to a new pin (see
  [Q2](#q2-how-to-distribute-the-envd-binary--resolved)) and cuts a
  release.
- End users run `edvabe pull-base` + `edvabe build-image --force` to
  pick up the new upstream image.
- Running sandboxes continue on the old image; new sandboxes use the new
  one.

Document the upgrade flow in `edvabe doctor --help`.
