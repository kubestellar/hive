#!/bin/bash
# Keeps the 'issue-scanner' tmux session alive 24/7. Supports both Claude Code
# and GitHub Copilot CLIs with automatic rate-limit failover.
#
# CLI selection: AGENT_CLI env var ("claude" or "copilot"), default "claude".
set -u

SESSION="issue-scanner"
WORKDIR="/home/dev/kubestellar-console"
POLL_SEC="${SCANNER_POLL_SEC:-10}"
READY_TIMEOUT_SEC="${SCANNER_READY_TIMEOUT_SEC:-45}"
# LOOP_PROMPT sourced from /etc/hive/scanner.env (AGENT_LOOP_PROMPT)
# Fall back to inline default if env file missing or var unset.
ENV_FILE="/etc/hive/scanner.env"
[ -f "$ENV_FILE" ] && . "$ENV_FILE"
LOOP_PROMPT="${AGENT_LOOP_PROMPT:-You are the KubeStellar Scanner in EXECUTOR MODE. Read project_scanner_policy.md from memory. Read your beads: cd /home/dev/scanner-beads && bd list --json. Wait for work orders from the supervisor.}"

# ─── CLI detection & failover ───────────────────────────────────────────────

CLAUDE_CMD="/usr/bin/claude --dangerously-skip-permissions --model claude-opus-4.6"
COPILOT_CMD="/usr/bin/copilot --allow-all"
ACTIVE_CLI="${AGENT_CLI:-claude}"

RATE_LIMIT_REGEX="usage limit|out of extra usage|Claude usage|rate limit reached|quota exhausted|monthly limit|Copilot usage limit|API rate limit|too many requests"

get_launch_cmd() {
  case "$ACTIVE_CLI" in
    copilot) echo "$COPILOT_CMD" ;;
    *)       echo "$CLAUDE_CMD" ;;
  esac
}

get_ready_marker() {
  case "$ACTIVE_CLI" in
    copilot) echo "Environment loaded" ;;
    *)       echo "bypass permissions on" ;;
  esac
}

get_other_cli() {
  case "$ACTIVE_CLI" in
    copilot) echo "claude" ;;
    *)       echo "copilot" ;;
  esac
}

# ─── Logging ────────────────────────────────────────────────────────────────

log() { printf '[%s] %s\n' "$(date -Is)" "$*"; }

# ─── Session lifecycle ──────────────────────────────────────────────────────

wait_for_ready() {
  local marker i
  marker=$(get_ready_marker)
  for ((i=0; i<READY_TIMEOUT_SEC; i++)); do
    if tmux capture-pane -t "$SESSION" -p 2>/dev/null | grep -qF "$marker"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

send_loop_prompt() {
  log "sending /loop prompt"
  tmux send-keys -t "$SESSION" -l "$LOOP_PROMPT"
  sleep 1
  tmux send-keys -t "$SESSION" Enter
}


CLAUDE_RENAME_TO="Scanner"
TMUX_STATUS_STYLE="bg=colour24,fg=white"
TMUX_PANE_BORDER_STYLE="fg=colour24"
TMUX_PANE_ACTIVE_BORDER_STYLE="fg=colour45"

apply_tmux_styling() {
  tmux set -t "$SESSION" status-style "$TMUX_STATUS_STYLE" 2>/dev/null || true
  tmux set -t "$SESSION" pane-border-style "$TMUX_PANE_BORDER_STYLE" 2>/dev/null || true
  tmux set -t "$SESSION" pane-active-border-style "$TMUX_PANE_ACTIVE_BORDER_STYLE" 2>/dev/null || true
}

send_claude_rename() {
  [ "$ACTIVE_CLI" = "copilot" ] && return 0
  [ -z "$CLAUDE_RENAME_TO" ] && return 0
  log "sending /rename $CLAUDE_RENAME_TO to claude"
  tmux send-keys -t "$SESSION" -l "/rename $CLAUDE_RENAME_TO"
  sleep 1
  tmux send-keys -t "$SESSION" Enter
  sleep 1
}

send_statusline() {
  [ "$ACTIVE_CLI" = "copilot" ] && return 0
  log "sending /statusline"
  tmux send-keys -t "$SESSION" -l "/statusline"
  sleep 1
  tmux send-keys -t "$SESSION" Enter
  sleep 2
}

start_session() {
  local launch_cmd
  launch_cmd=$(get_launch_cmd)
  log "starting tmux session $SESSION (cli=$ACTIVE_CLI, cmd=$launch_cmd)"
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  tmux new-session -d -s "$SESSION" -c "$WORKDIR" "$launch_cmd"
  apply_tmux_styling
  if wait_for_ready; then
    send_loop_prompt
    sleep 5
    send_claude_rename
    send_statusline

  else
    log "agent TUI did not become ready within ${READY_TIMEOUT_SEC}s; will retry on next tick"
  fi
}

# ─── Prompt handling ────────────────────────────────────────────────────────

approve_sensitive_prompt() {
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0
  if echo "$pane" | grep -qF "Yes, and always allow access to"; then
    log "auto-approving sensitive-file prompt (Down, Enter)"
    tmux send-keys -t "$SESSION" Down Enter
    sleep 3
  fi
}

dismiss_non_blocking_prompts() {
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0
  if echo "$pane" | grep -qF "How is Claude doing this session"; then
    log "auto-dismissing feedback prompt (Escape)"
    tmux send-keys -t "$SESSION" Escape
    sleep 2
  fi
}

# ─── Rate-limit detection & failover ────────────────────────────────────────

RATE_LIMIT_ALERTED=""

check_rate_limit_and_failover() {
  local pane topic other_cli
  topic="${NTFY_TOPIC:-issue-scanner}"
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0

  if echo "$pane" | grep -qiE "$RATE_LIMIT_REGEX"; then
    if [ -z "$RATE_LIMIT_ALERTED" ]; then
      other_cli=$(get_other_cli)
      log "rate limit detected on $ACTIVE_CLI; switching to $other_cli"

      curl -sS -m 10 \
        -H "Priority: high" \
        -H "Title: Scanner: rate limit on $ACTIVE_CLI → switching to $other_cli" \
        -H "Tags: rotating_light,key" \
        -d "Scanner at $(hostname) hit a rate limit on $ACTIVE_CLI. Auto-switching to $other_cli." \
        "https://ntfy.sh/$topic" >/dev/null || true

      ACTIVE_CLI="$other_cli"
      RATE_LIMIT_ALERTED="yes"
      start_session
    fi
  else
    if [ -n "$RATE_LIMIT_ALERTED" ]; then
      log "rate limit cleared; $ACTIVE_CLI is running"
      curl -sS -m 10 \
        -H "Priority: default" \
        -H "Title: Scanner: resumed on $ACTIVE_CLI" \
        -H "Tags: white_check_mark" \
        -d "Scanner resumed on $ACTIVE_CLI; rate limit cleared." \
        "https://ntfy.sh/$topic" >/dev/null || true
      RATE_LIMIT_ALERTED=""
    fi
  fi
}

# ─── Legacy notify (kept for backwards compat with the systemd env) ─────────

USAGE_LIMIT_ALERTED=""

notify_on_usage_limit() {
  # This is now handled by check_rate_limit_and_failover above.
  # Kept as a no-op so callers don't need updating.
  :
}

# ─── Liveness checks ───────────────────────────────────────────────────────

session_alive() { tmux has-session -t "$SESSION" 2>/dev/null; }

claude_alive() {
  local pids p
  pids=$(tmux list-panes -t "$SESSION" -F "#{pane_pid}" 2>/dev/null) || return 1
  [ -n "$pids" ] || return 1
  for p in $pids; do
    # Check for claude or copilot (or any child process)
    local cmd
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

# ─── Main loop ──────────────────────────────────────────────────────────────

trap 'log "supervisor exiting"; exit 0' TERM INT

log "supervisor started (cli=$ACTIVE_CLI, poll=${POLL_SEC}s, ready_timeout=${READY_TIMEOUT_SEC}s)"
start_session
while true; do
  if ! session_alive; then
    start_session
  elif ! claude_alive; then
    log "agent not running in $SESSION; restarting"
    start_session
  else
    approve_sensitive_prompt
    dismiss_non_blocking_prompts
    check_rate_limit_and_failover
  fi
  sleep "$POLL_SEC"
done
