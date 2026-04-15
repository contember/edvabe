#!/usr/bin/env bash
# Smoke test: verify upstream envd works in plain Docker.
#
# Boots edvabe/base:latest, hits /health, POSTs /init, and makes a single
# process.Process/Start Connect-RPC call that runs `echo hello`. Verifies
# the stream contains a StartEvent and an EndEvent. Resolves Q3 from
# docs/07-open-questions.md.
#
# Usage: ./test/smoke/envd_in_docker.sh
# Exit:  0 on success, non-zero on any step failure.
#
# Prerequisites: Docker, curl, python3, and edvabe/base:latest built
# (run `go run ./cmd/edvabe build-image` first).

set -euo pipefail

CONTAINER_NAME="edvabe-envd-smoke"
IMAGE="edvabe/base:latest"
HOST_PORT="${EDVABE_SMOKE_PORT:-49983}"
TOKEN="ea_smoketoken"

log()  { printf '\033[36m[smoke]\033[0m %s\n' "$*"; }
fail() { printf '\033[31m[smoke] FAIL:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
    docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "step 1/5: preflight"
command -v docker  >/dev/null || fail "docker not on PATH"
command -v curl    >/dev/null || fail "curl not on PATH"
command -v python3 >/dev/null || fail "python3 not on PATH"
docker image inspect "${IMAGE}" >/dev/null 2>&1 \
    || fail "${IMAGE} not built — run 'go run ./cmd/edvabe build-image' first"

log "step 2/5: start container"
cleanup
docker run -d --name "${CONTAINER_NAME}" -p "${HOST_PORT}:49983" "${IMAGE}" >/dev/null

# envd boots almost immediately but give it a few tries before giving up.
for i in 1 2 3 4 5 6 7 8 9 10; do
    code=$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost:${HOST_PORT}/health" 2>/dev/null || echo '000')
    if [ "${code}" = "204" ]; then
        log "  /health → 204 (after ${i} attempt$( [ "$i" -gt 1 ] && echo s))"
        break
    fi
    sleep 0.5
    if [ "$i" = "10" ]; then
        log "envd logs:"
        docker logs "${CONTAINER_NAME}" 2>&1 | sed 's/^/  | /'
        fail "/health never returned 204"
    fi
done

log "step 3/5: POST /init"
TS=$(python3 -c 'from datetime import datetime, timezone; print(datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.") + f"{datetime.now(timezone.utc).microsecond//1000:03d}Z")')
INIT_BODY=$(python3 -c "
import json, sys
print(json.dumps({
    'accessToken': '${TOKEN}',
    'envVars': {'SMOKE': '1'},
    'timestamp': '${TS}',
    'defaultUser': 'user',
    'defaultWorkdir': '/home/user',
}))
")
init_code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' \
    -d "${INIT_BODY}" \
    "http://localhost:${HOST_PORT}/init")
[ "${init_code}" = "204" ] || fail "/init returned ${init_code}, want 204"
log "  /init → 204"

log "step 4/5: process.Process/Start (echo hello)"
PYOUT=$(PORT="${HOST_PORT}" TOKEN="${TOKEN}" python3 <<'PY'
import json, os, struct, sys, urllib.request, urllib.error

body = json.dumps({
    "process": {
        "cmd": "/bin/sh",
        "args": ["-c", "echo hello"],
        "envs": {},
        "cwd": "/home/user",
    },
}).encode()
envelope = b"\x00" + struct.pack(">I", len(body)) + body
req = urllib.request.Request(
    f"http://localhost:{os.environ['PORT']}/process.Process/Start",
    data=envelope,
    method="POST",
    headers={
        "Content-Type": "application/connect+json",
        "Connect-Protocol-Version": "1",
        "X-Access-Token": os.environ["TOKEN"],
    },
)
try:
    with urllib.request.urlopen(req, timeout=10) as r:
        status = r.status
        raw = r.read()
except urllib.error.HTTPError as e:
    print(f"HTTP_ERROR status={e.code} body={e.read().decode(errors='replace')}", file=sys.stderr)
    sys.exit(1)

if status != 200:
    print(f"unexpected status {status}", file=sys.stderr)
    sys.exit(1)

events = []
off = 0
while off < len(raw):
    flags = raw[off]
    length = struct.unpack(">I", raw[off+1:off+5])[0]
    payload = raw[off+5:off+5+length]
    try:
        parsed = json.loads(payload.decode())
    except Exception:
        parsed = {"_raw": repr(payload[:64])}
    events.append({"flags": f"0x{flags:02x}", "length": length, "payload": parsed})
    off += 5 + length

print(json.dumps(events))
PY
)
echo "${PYOUT}" | python3 -c "
import json, sys
events = json.loads(sys.stdin.read())
for e in events:
    print(f\"  frame {e['flags']} len={e['length']} {e['payload']}\")
kinds = set()
for e in events:
    ev = e['payload'].get('event', {}) if isinstance(e['payload'], dict) else {}
    kinds.update(ev.keys())
if 'start' not in kinds:
    print('FAIL: no StartEvent in stream', file=sys.stderr); sys.exit(1)
if 'end' not in kinds:
    print('FAIL: no EndEvent in stream', file=sys.stderr); sys.exit(1)
print('  ok: stream contains start + end events')
"

log "step 5/5: cleanup"
cleanup
trap - EXIT

log "PASS — envd runs in plain Docker"
