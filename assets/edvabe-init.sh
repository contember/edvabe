#!/bin/sh
# edvabe-init — PID 1 for user sandbox containers.
#
# The real template builder rewrites every generated Dockerfile's CMD
# to ["/usr/local/bin/edvabe-init"], so this script is the container's
# long-lived foreground process. Its job:
#
#   1. Launch envd in the background — the reverse proxy talks to it.
#   2. If EDVABE_START_CMD is set, launch the user's template startCmd
#      alongside envd (fire-and-forget; supervision is deliberately
#      dumb — if the user command dies envd stays up, and if envd
#      dies the container exits and the sandbox manager notices).
#   3. wait on envd so the container stays alive as long as envd does.
#
# EDVABE_READY_CMD is not used here — the sandbox manager runs it
# through envd's process RPC after InitAgent succeeds. The variable is
# still plumbed into the container's env for diagnostics.

set -e

/usr/local/bin/envd --isnotfc &
ENVD_PID=$!

if [ -n "$EDVABE_START_CMD" ]; then
    sh -c "$EDVABE_START_CMD" &
fi

wait "$ENVD_PID"
