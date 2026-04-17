#!/bin/bash
# Main supervisor loop. Keeps the tmux session alive and the agent running
# inside it. Re-sends AGENT_LOOP_PROMPT on every spawn. Auto-approves a known
# sensitive-file prompt pattern (optional). Systemd EnvironmentFile supplies
# all configuration.
set -u

: "${AGENT_SESSION_NAME:?AGENT_SESSION_NAME is required}"
: "${AGENT_WORKDIR:?AGENT_WORKDIR is required}"
: "${AGENT_LOOP_PROMPT:?AGENT_LOOP_PROMPT is required}"

SESSION="$AGENT_SESSION_NAME"
WORKDIR="$AGENT_WORKDIR"
LAUNCH="/usr/local/bin/agent-launch.sh"
POLL_SEC="${AGENT_POLL_SEC:-10}"
READY_TIMEOUT_SEC="${AGENT_READY_TIMEOUT_SEC:-45}"
READY_MARKER="${AGENT_READY_MARKER:-bypass permissions on}"
AUTO_APPROVE_PHRASE="${AGENT_AUTO_APPROVE_PHRASE:-}"

log() { printf '[%s] %s\n' "$(date -Is)" "$*"; }

wait_for_ready() {
  local i
  for ((i = 0; i < READY_TIMEOUT_SEC; i++)); do
    if tmux capture-pane -t "$SESSION" -p 2>/dev/null | grep -qF "$READY_MARKER"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

send_loop_prompt() {
  log "sending AGENT_LOOP_PROMPT"
  # -l sends the string literally (no key-name translation), Enter on a separate call.
  tmux send-keys -t "$SESSION" -l "$AGENT_LOOP_PROMPT"
  sleep 1
  tmux send-keys -t "$SESSION" Enter
}

start_session() {
  log "starting tmux session $SESSION"
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  tmux new-session -d -s "$SESSION" -c "$WORKDIR" "$LAUNCH"
  if wait_for_ready; then
    send_loop_prompt
  else
    log "agent TUI did not show ready marker within ${READY_TIMEOUT_SEC}s; will retry on next tick"
  fi
}

approve_prompt_if_present() {
  # No-op if AUTO_APPROVE_PHRASE is blank.
  [ -z "$AUTO_APPROVE_PHRASE" ] && return 0
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0
  if echo "$pane" | grep -qF "$AUTO_APPROVE_PHRASE"; then
    log "auto-approving pending prompt (Down, Enter)"
    tmux send-keys -t "$SESSION" Down Enter
    sleep 3
  fi
}

session_alive() { tmux has-session -t "$SESSION" 2>/dev/null; }

agent_alive() {
  local pids p cmd
  pids=$(tmux list-panes -t "$SESSION" -F "#{pane_pid}" 2>/dev/null) || return 1
  [ -n "$pids" ] || return 1
  # Child PIDs of the pane: consider the agent alive if any child is running.
  for p in $pids; do
    cmd=$(ps -p "$p" -o comm= 2>/dev/null)
    if [ -n "$cmd" ] && [ "$cmd" != "bash" ] && [ "$cmd" != "sh" ]; then
      return 0
    fi
    if pgrep -P "$p" >/dev/null 2>&1; then
      return 0
    fi
  done
  return 1
}

trap 'log "supervisor exiting"; exit 0' TERM INT

log "supervisor started (session=$SESSION, poll=${POLL_SEC}s, ready_timeout=${READY_TIMEOUT_SEC}s)"
while true; do
  if ! session_alive; then
    start_session
  elif ! agent_alive; then
    log "agent process missing in $SESSION; restarting"
    start_session
  else
    approve_prompt_if_present
  fi
  sleep "$POLL_SEC"
done
