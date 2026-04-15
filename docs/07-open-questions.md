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

**Decided**: Phase 1 combines Q2 options (D) and (B) with a twist. Task 4
pulls E2B's multi-arch `e2bdev/base` as the runtime base; task 5 builds
`edvabe/base:latest` as a multi-stage Docker image that compiles
upstream envd from source at a pinned `e2b-dev/infra` commit and layers
it onto `e2bdev/base`. The Go toolchain lives inside Docker (via the
`golang:1.24` builder stage), not on the host — sidestepping (B)'s
original objection to host Go.

**Why this combination.** Investigation on 2026-04-15, in two passes:

**Pass 1.**
- (A) is impossible — `e2b-dev/infra` GitHub releases carry zero assets.
  envd is uploaded to E2B's private GCP/AWS buckets (see
  `packages/envd/Makefile` upload target), not to GitHub.
- (B) was out per the original Q2 write-up — Go toolchain on the host is
  too heavy a dependency for a `go install` UX.
- (C) would still need a source for the binary, inheriting (A)'s problem.
- (D) as originally framed meant edvabe publishing its own image, but
  E2B already publishes `e2bdev/base` multi-arch (amd64 + arm64), ~470 MB
  per arch. We initially chose to consume it directly.

**Pass 2 (at task 6 kickoff).** Inspecting `e2bdev/base` inside a
container showed it does NOT contain envd — `find / -name envd` returns
nothing and the default `CMD` is `python3`. `e2bdev/code-interpreter`
is identical in this respect. Neither public image ships envd. E2B's
orchestrator injects envd into sandbox images outside of what they
publish to Docker Hub.

This forced the hybrid: keep (D) for the runtime base (which has
Python, Node, and the tooling E2B's SDK expects) but layer an
envd-from-source stage on top. The host only needs Docker; Docker's
`golang:1.24` image provides the Go toolchain during `build-image`, so
(B)'s objection doesn't apply.

**Pin strategy.**
- `BaseImageDigest` pins `e2bdev/base` by OCI index digest. Bump by
  HEAD-requesting `e2bdev/base:latest` and updating the const.
- `EnvdSourceSHA` pins the `e2b-dev/infra` commit from which envd is
  built. Bump by picking a new commit from GitHub (typically the HEAD
  of the latest weekly release tag) and updating the const.
- `DefaultEnvdVersion` is still `"0.5.7"` — it's the value reported to
  SDKs in Sandbox responses (CLAUDE.md golden rule #3), not tied to
  the source pin.

## Q3: Does upstream envd actually run cleanly in a plain Docker container? — *resolved*

**Decided**: yes, with `-isnotfc`, plain `docker run`, no special
capabilities, no bind mounts. Verified 2026-04-15 by
`test/smoke/envd_in_docker.sh` — runs `edvabe/base:latest`, hits
`/health`, POSTs `/init`, and makes one `process.Process/Start`
Connect-RPC call. All four steps pass.

**Concerns from the original write-up, revisited:**

- **cgroups v2 setup** — envd logs three warnings on boot:
  ```
  failed to create cgroup2 manager: ... mkdir /sys/fs/cgroup/user: read-only file system
  failed to create pty cgroup: ... mkdir /sys/fs/cgroup/ptys: read-only file system
  failed to create socat cgroup: ... mkdir /sys/fs/cgroup/socats: read-only file system
  falling back to no-op cgroup manager
  ```
  Benign. `--cap-add=SYS_ADMIN` or `--privileged` would clear them but
  functionality (process start, stdout streaming, exit reporting) works
  fine with the no-op manager. Documenting rather than suppressing —
  if cgroup warnings start affecting functionality in a future envd
  release we want to see them.

- **MMDS polling** — `-isnotfc` short-circuits the MMDS path
  (`a.isNotFC` check in `packages/envd/internal/api/init.go`). No
  observed log spam, no functional impact.

- **Port forwarder via socat** — not exercised by this smoke test. The
  `/health`, `/init`, and `process.Process/Start` paths don't touch it.
  Revisit if a Phase 1 or Phase 2 task needs user-bound sandbox ports;
  expect the in-VM gateway IP (`169.254.0.21`) won't exist in Docker
  bridge networks so socat will likely fail loudly when first used.
  Track as a **known gap** — not a Phase 1 blocker.

- **Systemd time setting** — envd's `/init` validates the request
  timestamp (`maxTimeInPast = 50ms`, `maxTimeInFuture = 5s`) but under
  `-isnotfc` it does not attempt to `clock_settime`. The smoke test
  sends `now` and gets 204. No `CAP_SYS_TIME` required.

**Related finding (fixed in the same commit).** `e2bdev/base` ships
only root; no `user` account. E2B SDKs default to `DefaultUser="user"`
and `DefaultWorkdir="/home/user"` ([docs/05-architecture.md](05-architecture.md)),
so edvabe's `/init` call would get `invalid default user: 'user'` from
envd. `assets/Dockerfile.base` now adds the `user` account with
passwordless sudo (mirrors `debug.Dockerfile` in upstream envd). Not
technically a Q3 concern but discovered during this smoke run.

**Known gaps, tracked for later:**
- socat port forwarder (see above).
- `ptys` / `socats` cgroup subtrees — functionality not proved for
  interactive PTY processes. Smoke test uses non-PTY `echo hello`.

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

## Q15: Back-compat with older E2B SDK versions

**The question.** v0.1.0 E2E tests pin `e2b==2.20.0` (Python) and
`e2b@2.19.0` (TS). The internal **webmaster** project is on
`e2b@2.6.0` + `@e2b/code-interpreter@2.3.3`. Do we support all SDK
versions back to (at least) 2.0.0, or only the "current" 2.19+?

**Why it matters.** If wire shapes drifted between 2.6 and 2.19 —
field renames, new required fields, status-code flips like the 200 →
201 we hit in task 12 — a webmaster dropin fails even after pause/
resume lands in Phase 3. The symptom will look like our task-12
"`parsed=None`" error: silent failure deep inside the SDK's generated
client rather than an edvabe-side error.

**Current leaning.** Before starting Phase 3 (or the earliest phase
that targets webmaster compat), run the Python + TS E2E suites against
the old SDK versions webmaster pins, note every divergence, and pick a
"minimum supported SDK" bound in CHANGELOG. If a divergence is small
(status code, field name), fix edvabe to serve both shapes. If it's a
semantic change (e.g. `betaCreate` body differs), pin webmaster forward
instead — it's a first-party project.

**Confirm when.** Task-boundary check at the start of Phase 2 or 3,
whichever targets webmaster first.

## Q16: `Template.build()` wire shape

**The question.** Webmaster's `containers/templates/chrome/build.ts`
uses the JS SDK's `Template.build({ alias, memoryMB, … })` API to
build the `webmaster-sandbox-chrome` image. Phase 3's
`POST /v3/templates → /v2/templates/{id}/builds/{buildID}` is designed
against the OpenAPI `TemplateBuildStartV2` schema. Do those two
actually line up on the wire?

**Why it matters.** Phase 3's template builder is a
~1000-LOC subpackage. If `Template.build()` sends something other than
`TemplateBuildStartV2` (maybe: a different path, a different envelope,
a streaming upload of the local Docker build context) we burn most of
that budget on the wrong wire. And we won't know until a webmaster
build command fails halfway through.

**Current leaning.** Before writing any builder code in Phase 3,
intercept one `Template.build()` call from webmaster (tcpdump against
`cloud.e2b.dev`, or patch the SDK locally to log the request) and
compare against `TemplateBuildStartV2` in the OpenAPI spec. If they
diverge, the divergence goes into this question's resolution notes
and Phase 3 scope gets updated before any implementation starts.

**Confirm when.** Phase 3 entry, before touching `internal/template/`.
