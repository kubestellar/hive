#!/bin/bash
set -euo pipefail

# ttyd runs as root but tmux sessions are owned by per-agent UIDs.
# Agents are launched with `tmux -L hive-{name}` under `su-exec hive-{name}`,
# creating sockets at /tmp/tmux-{agent_uid}/hive-{name}. Since agent UIDs
# vary (2001-2005+), we search across all /tmp/tmux-*/ directories for the
# matching socket. For agents without UID isolation (e.g. supervisor running
# as dev), the session lives on the default socket at /tmp/tmux-{dev_uid}/default.

SESSION=${1:-supervisor}

# Find the per-agent socket: /tmp/tmux-*/${SESSION}
# The glob matches across all UID directories.
TMUX_SOCKET=""
for sock in /tmp/tmux-*/"${SESSION}"; do
  if [ -S "$sock" ]; then
    TMUX_SOCKET="$sock"
    break
  fi
done

# Fallback: session may be on the default tmux server (no UID isolation).
if [ -z "$TMUX_SOCKET" ]; then
  DEV_UID=$(id -u dev 2>/dev/null || echo "1001")
  TMUX_SOCKET="/tmp/tmux-${DEV_UID}/default"
fi

if [ ! -S "$TMUX_SOCKET" ]; then
  echo "error: no tmux socket found for session '${SESSION}'" >&2
  exit 1
fi

PREV_MOUSE=$(tmux -S "$TMUX_SOCKET" show-option -t "$SESSION" -v mouse 2>/dev/null || echo "on")
tmux -S "$TMUX_SOCKET" set-option -t "$SESSION" mouse off 2>/dev/null || true
EXIT_CODE=0
tmux -S "$TMUX_SOCKET" attach-session -t "$SESSION" || EXIT_CODE=$?
tmux -S "$TMUX_SOCKET" set-option -t "$SESSION" mouse "$PREV_MOUSE" 2>/dev/null || true
exit $EXIT_CODE
