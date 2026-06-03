#!/bin/bash
# contributor-agent.sh — Entrypoint for the contributor container.
#
# 1. Detects which CLI backend is authenticated
# 2. Starts the contributor-relay (WebSocket client) in the background
# 3. Launches the CLI agent in a tmux session
# 4. The relay feeds tasks into the tmux session and reports results
#
# Environment (from ~/.config/hive/contributor.env):
#   HIVE_HUB                — WebSocket URL
#   HIVE_REGISTRATION_TOKEN — contributor's token
#   AGENT_BACKEND           — preferred CLI backend

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG_DIR="${HOME}/.config/hive"
CONFIG_FILE="${CONFIG_DIR}/contributor.env"
TMUX_SESSION="contributor"

# Load contributor config
if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck source=/dev/null
  source "$CONFIG_FILE"
fi

export HIVE_HUB="${HIVE_HUB:-wss://hive.kubestellar.io:3001/contribute}"
export HIVE_REGISTRATION_TOKEN="${HIVE_REGISTRATION_TOKEN:?Not registered — run 'just contribute-register' first}"
export AGENT_BACKEND="${AGENT_BACKEND:-claude}"
export HIVE_AGENT_SESSION="$TMUX_SESSION"
export HIVE_AGENT_ID="contributor"
export HIVE_CONTRIBUTOR_MODE="true"
export HIVE_CONTRIBUTOR_CLI="$AGENT_BACKEND"
# Username is extracted from contributor.env (set during registration)
export HIVE_CONTRIBUTOR_USERNAME="${CONTRIBUTOR_USERNAME:-unknown}"

# Source backends.conf for binary detection
BACKENDS_CONF="${SCRIPT_DIR}/../config/backends.conf"
if [[ -f "$BACKENDS_CONF" ]]; then
  source "$BACKENDS_CONF"
elif [[ -f /usr/local/etc/hive/backends.conf ]]; then
  source /usr/local/etc/hive/backends.conf
fi

# Detect CLI authentication
detect_cli() {
  local backend="$1"
  local cmd
  cmd=$(backend_binary "$backend" 2>/dev/null || echo "$backend")

  if ! command -v "$cmd" &>/dev/null; then
    echo "NOT_INSTALLED"
    return
  fi

  case "$backend" in
    claude)
      if claude --version &>/dev/null; then echo "OK"; else echo "NOT_AUTHED"; fi
      ;;
    copilot)
      if copilot --version &>/dev/null; then echo "OK"; else echo "NOT_AUTHED"; fi
      ;;
    gemini)
      if gemini --version &>/dev/null; then echo "OK"; else echo "NOT_AUTHED"; fi
      ;;
    bob)
      if bob --version &>/dev/null; then echo "OK"; else echo "NOT_AUTHED"; fi
      ;;
    goose)
      if goose --version &>/dev/null; then echo "OK"; else echo "NOT_AUTHED"; fi
      ;;
    *)
      echo "UNKNOWN"
      ;;
  esac
}

echo "=== Hive Contributor Agent ==="
echo "Hub:     $HIVE_HUB"
echo "Backend: $AGENT_BACKEND"
echo ""

# Check CLI readiness
STATUS=$(detect_cli "$AGENT_BACKEND")
case "$STATUS" in
  NOT_INSTALLED)
    echo "ERROR: $AGENT_BACKEND CLI not found."
    echo "Install it and try again."
    exit 1
    ;;
  NOT_AUTHED)
    echo "WARNING: $AGENT_BACKEND CLI may not be authenticated."
    echo "You may need to log in. The agent will start and prompt if needed."
    ;;
  OK)
    echo "$AGENT_BACKEND CLI detected and ready."
    ;;
esac

# Ensure metrics directory exists for gh-wrapper token injection
mkdir -p /var/run/hive-metrics 2>/dev/null || true

# Create tmux session for the agent
tmux kill-session -t "$TMUX_SESSION" 2>/dev/null || true
tmux new-session -d -s "$TMUX_SESSION" -x 200 -y 50

# Start the relay in the background
echo "Starting relay connection to hub..."
node "${SCRIPT_DIR}/contributor-relay.sh" &
RELAY_PID=$!


# Launch the CLI in the tmux session
CMD=$(backend_binary "$AGENT_BACKEND")
PERM_FLAG=$(backend_perm_flag "$AGENT_BACKEND")
MODEL_FLAG=""
if [[ -n "${AGENT_MODEL:-}" ]]; then
  case "$AGENT_BACKEND" in
    amazonq|goose) ;;
    *) MODEL_FLAG="--model $AGENT_MODEL" ;;
  esac
fi

# Ensure Claude Code skips onboarding by injecting required flags
if [[ "$AGENT_BACKEND" == "claude" ]] && [[ -f "${HOME}/.claude.json" ]]; then
  python3 -c "
import json, sys
p = '${HOME}/.claude.json'
with open(p) as f: d = json.load(f)
d['hasCompletedOnboarding'] = True
d['lastOnboardingVersion'] = '2.1.0'
d.setdefault('numStartups', 1)
with open(p, 'w') as f: json.dump(d, f, indent=2)
" 2>/dev/null || true
fi

tmux send-keys -t "$TMUX_SESSION" "$CMD $PERM_FLAG $MODEL_FLAG" Enter

echo ""
CONTAINER_NAME="${HIVE_CONTAINER_NAME:-hive-contributor}"
echo "Contributor agent is running."
echo "  CLI:   $CMD"
echo "  Relay: PID $RELAY_PID"
echo "  Tmux:  docker exec -it $CONTAINER_NAME tmux attach -t $TMUX_SESSION"
echo ""
echo "Press Ctrl-C to stop contributing."

# Keep running until interrupted
cleanup() {
  echo "Shutting down..."
  kill "$RELAY_PID" 2>/dev/null || true
  tmux kill-session -t "$TMUX_SESSION" 2>/dev/null || true
  exit 0
}
trap cleanup SIGTERM SIGINT

wait "$RELAY_PID"
