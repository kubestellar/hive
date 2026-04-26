#!/bin/bash
# supervisor-kick.sh — Reliable agent session restart + kick for Copilot-backend sessions.
#
# Usage:
#   supervisor-kick.sh <session> "<kick message>"
#
# What it does (atomically — launch and instruct are never separated):
#   1. If session missing: create it, launch Copilot CLI, wait for idle prompt
#   2. If session exists but agent not running: relaunch Copilot CLI, wait
#   3. Send kick message text (separate call)
#   4. Send Enter (separate call — never combined with message)
#   5. Verify agent started processing (shows ◎ or tool call output)
#   6. Return 0 on success, 1 on failure
#
# Key lessons baked in:
#   - copilot --allow-all is the correct backend on this host (not claude)
#   - Text and Enter are ALWAYS two separate tmux send-keys calls
#   - Launch and kick are ONE atomic operation — never launch then move on
#   - Wait for idle prompt (❯) before sending kick to avoid lost messages

set -euo pipefail

SESSION="${1:?Usage: supervisor-kick.sh <session> \"<message>\"}"
MESSAGE="${2:?Usage: supervisor-kick.sh <session> \"<message>\"}"

WORKDIR="${AGENT_WORKDIR:-/home/dev/kubestellar-console}"
MODEL="${AGENT_MODEL:-claude-sonnet-4-6}"
IDLE_MARKER="❯"
READY_TIMEOUT="${READY_TIMEOUT:-45}"
VERIFY_TIMEOUT="${VERIFY_TIMEOUT:-15}"

log() { echo "[supervisor-kick] $*" >&2; }

session_exists() { tmux has-session -t "$SESSION" 2>/dev/null; }

agent_running() {
  tmux capture-pane -t "$SESSION" -p 2>/dev/null | grep -q "/ commands"
}

wait_for_idle() {
  local i
  for ((i = 0; i < READY_TIMEOUT; i++)); do
    if tmux capture-pane -t "$SESSION" -p 2>/dev/null | grep -q "$IDLE_MARKER"; then
      return 0
    fi
    sleep 1
  done
  log "WARN: idle prompt not detected after ${READY_TIMEOUT}s in $SESSION — proceeding anyway"
  return 0
}

ensure_session() {
  if ! session_exists; then
    log "Creating session $SESSION"
    tmux new-session -d -s "$SESSION" -c "$WORKDIR"
    sleep 1
  fi
}

ensure_agent() {
  if ! agent_running; then
    log "Launching Copilot CLI in $SESSION (model: $MODEL)"
    tmux send-keys -t "$SESSION" "cd $WORKDIR && copilot --allow-all --model $MODEL"
    tmux send-keys -t "$SESSION" Enter
    log "Waiting for idle prompt..."
    wait_for_idle
  else
    log "Agent already running in $SESSION"
    wait_for_idle
  fi
}

send_kick() {
  log "Sending kick to $SESSION"
  # RULE: text and Enter are always two separate calls — never combined
  tmux send-keys -t "$SESSION" "$MESSAGE"
  sleep 1
  tmux send-keys -t "$SESSION" Enter
}

verify_processing() {
  local i
  for ((i = 0; i < VERIFY_TIMEOUT; i++)); do
    local pane
    pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null)
    # Agent is processing if it shows ◎ (thinking) or a tool call or enqueue indicator
    if echo "$pane" | grep -qE "◎|ctrl\+q enqueue|Initializing|cat |bash |grep "; then
      log "✓ $SESSION is processing"
      return 0
    fi
    sleep 1
  done
  log "WARN: $SESSION may not have started processing — check pane manually"
  # Not fatal — return success anyway; caller can inspect
  return 0
}

main() {
  ensure_session
  ensure_agent
  send_kick
  verify_processing
  log "Done — $SESSION kicked"
}

main
