#!/bin/bash
set -euo pipefail

# ttyd runs as root but tmux sessions are owned by dev (uid 1001).
# TMUX_TMPDIR doesn't work here because tmux appends tmux-$UID/ to it,
# producing the wrong path. Use -S with the exact socket path instead.
DEV_UID=$(id -u dev 2>/dev/null || echo "1001")
TMUX_SOCKET="/tmp/tmux-${DEV_UID}/default"

SESSION=${1:-supervisor}
PREV_MOUSE=$(tmux -S "$TMUX_SOCKET" show-option -t "$SESSION" -v mouse 2>/dev/null || echo "on")
tmux -S "$TMUX_SOCKET" set-option -t "$SESSION" mouse off 2>/dev/null || true
EXIT_CODE=0
tmux -S "$TMUX_SOCKET" attach-session -t "$SESSION" || EXIT_CODE=$?
tmux -S "$TMUX_SOCKET" set-option -t "$SESSION" mouse "$PREV_MOUSE" 2>/dev/null || true
exit $EXIT_CODE
