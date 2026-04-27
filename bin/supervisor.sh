#!/bin/bash
# Unified supervisor — keeps a tmux session alive with an AI CLI agent inside.
# Uses the gastown pattern: prompt is passed as a CLI argument, not via
# tmux send-keys, eliminating the startup race condition.
#
# Supports optional rate-limit failover between CLI backends (set
# AGENT_RATE_LIMIT_FAILOVER=true and AGENT_FAILOVER_CMD in env).
set -u

: "${AGENT_SESSION_NAME:?AGENT_SESSION_NAME is required}"
: "${AGENT_WORKDIR:?AGENT_WORKDIR is required}"
: "${AGENT_LOOP_PROMPT:?AGENT_LOOP_PROMPT is required}"
: "${AGENT_LAUNCH_CMD:?AGENT_LAUNCH_CMD is required}"

SESSION="$AGENT_SESSION_NAME"
WORKDIR="$AGENT_WORKDIR"
POLL_SEC="${AGENT_POLL_SEC:-10}"
CLI="${AGENT_CLI:-claude}"
AUTO_APPROVE_PHRASE="${AGENT_AUTO_APPROVE_PHRASE:-}"
AUTO_DISMISS_PHRASES="${AGENT_AUTO_DISMISS_PHRASES:-}"
NOTIFY_ON_PHRASE_REGEX="${AGENT_NOTIFY_ON_PHRASE_REGEX:-}"
NOTIFY_ON_PHRASE_TITLE="${AGENT_NOTIFY_ON_PHRASE_TITLE:-Agent needs attention}"
NOTIFY_ON_PHRASE_BODY="${AGENT_NOTIFY_ON_PHRASE_BODY:-Matched notify phrase in session $SESSION on $(hostname).}"
TMUX_STATUS_STYLE="${AGENT_TMUX_STATUS_STYLE:-}"
TMUX_PANE_BORDER_STYLE="${AGENT_TMUX_PANE_BORDER_STYLE:-}"
TMUX_PANE_ACTIVE_BORDER_STYLE="${AGENT_TMUX_PANE_ACTIVE_BORDER_STYLE:-}"
CLAUDE_RENAME_TO="${AGENT_CLAUDE_RENAME_TO:-}"

RATE_LIMIT_FAILOVER="${AGENT_RATE_LIMIT_FAILOVER:-false}"
FAILOVER_CMD="${AGENT_FAILOVER_CMD:-}"
FAILOVER_CLI="${AGENT_FAILOVER_CLI:-}"
RATE_LIMIT_REGEX="${AGENT_RATE_LIMIT_REGEX:-out of extra usage|Claude usage limit|Copilot usage limit|monthly limit|quota exhausted}"
RATE_LIMIT_COOLDOWN_SEC="${AGENT_RATE_LIMIT_COOLDOWN_SEC:-300}"
LAST_SWITCH_EPOCH=0
RATE_LIMIT_ALERTED=""

ACTIVE_LAUNCH_CMD="$AGENT_LAUNCH_CMD"

log() { printf '[%s] %s\n' "$(date -Is)" "$*"; }

# ─── Write launcher script (avoids quoting hell with eval) ─────────────────

LAUNCHER="/tmp/.supervisor-launch-${SESSION}.sh"

write_launcher() {
  local prompt_file="/tmp/.supervisor-prompt-${SESSION}.txt"
  printf '%s' "$AGENT_LOOP_PROMPT" > "$prompt_file"

  case "$CLI" in
    claude)
      cat > "$LAUNCHER" << LAUNCH
#!/bin/bash
cd "$WORKDIR"
exec $ACTIVE_LAUNCH_CMD "\$(cat $prompt_file)"
LAUNCH
      ;;
    copilot)
      cat > "$LAUNCHER" << LAUNCH
#!/bin/bash
cd "$WORKDIR"
exec $ACTIVE_LAUNCH_CMD -i "\$(cat $prompt_file)"
LAUNCH
      ;;
    gemini)
      cat > "$LAUNCHER" << LAUNCH
#!/bin/bash
cd "$WORKDIR"
exec $ACTIVE_LAUNCH_CMD -i "\$(cat $prompt_file)"
LAUNCH
      ;;
    goose)
      cat > "$LAUNCHER" << LAUNCH
#!/bin/bash
cd "$WORKDIR"
exec $ACTIVE_LAUNCH_CMD --prompt "\$(cat $prompt_file)"
LAUNCH
      ;;
    *)
      cat > "$LAUNCHER" << LAUNCH
#!/bin/bash
cd "$WORKDIR"
exec $ACTIVE_LAUNCH_CMD "\$(cat $prompt_file)"
LAUNCH
      ;;
  esac
  chmod +x "$LAUNCHER"
}

# ─── Tmux styling ─────────────────────────────────────────────────────────

apply_tmux_styling() {
  [ -n "$TMUX_STATUS_STYLE" ] && tmux set -t "$SESSION" status-style "$TMUX_STATUS_STYLE" 2>/dev/null || true
  [ -n "$TMUX_PANE_BORDER_STYLE" ] && tmux set -t "$SESSION" pane-border-style "$TMUX_PANE_BORDER_STYLE" 2>/dev/null || true
  [ -n "$TMUX_PANE_ACTIVE_BORDER_STYLE" ] && tmux set -t "$SESSION" pane-active-border-style "$TMUX_PANE_ACTIVE_BORDER_STYLE" 2>/dev/null || true
}

# ─── Session lifecycle ─────────────────────────────────────────────────────

PROMPT_DELIVERED=""

start_session() {
  write_launcher
  log "starting tmux session $SESSION (cli=$CLI)"
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  PROMPT_DELIVERED=""
  tmux new-session -d -s "$SESSION" -c "$WORKDIR" "$LAUNCHER"
  apply_tmux_styling
}

# ─── Startup dialog handling ──────────────────────────────────────────────

handle_startup_dialogs() {
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0

  if echo "$pane" | grep -qF "Do you trust the files"; then
    log "auto-approving folder trust dialog (Down, Enter)"
    tmux send-keys -t "$SESSION" Down Enter
    sleep 2
    return 0
  fi

  if echo "$pane" | grep -qF "bypass permissions on" && echo "$pane" | grep -qF "shift+tab to cycle"; then
    log "accepting bypass permissions prompt (Enter)"
    tmux send-keys -t "$SESSION" Enter
    sleep 2
    return 0
  fi
}

check_prompt_delivered() {
  [ -n "$PROMPT_DELIVERED" ] && return 0
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 1
  if echo "$pane" | grep -qE '◐|◑|◒|◓|● Read CLAUDE|● Environment loaded.*custom instructions'; then
    PROMPT_DELIVERED="yes"
    log "prompt delivered and agent is working"
    if [ "$CLI" = "claude" ] && [ -n "$CLAUDE_RENAME_TO" ]; then
      sleep 5
      log "sending /rename $CLAUDE_RENAME_TO"
      tmux send-keys -t "$SESSION" -l "/rename $CLAUDE_RENAME_TO"
      sleep 1
      tmux send-keys -t "$SESSION" Enter
    fi
    return 0
  fi
  return 1
}

# ─── Prompt handling ──────────────────────────────────────────────────────

approve_prompt_if_present() {
  [ -z "$AUTO_APPROVE_PHRASE" ] && return 0
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0
  if echo "$pane" | grep -qF "$AUTO_APPROVE_PHRASE"; then
    log "auto-approving pending prompt (Down, Enter)"
    tmux send-keys -t "$SESSION" Down Enter
    sleep 3
  fi
}

dismiss_prompts_if_present() {
  [ -z "$AUTO_DISMISS_PHRASES" ] && return 0
  local pane phrase
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0
  while IFS= read -r phrase; do
    [ -z "$phrase" ] && continue
    if echo "$pane" | grep -qF "$phrase"; then
      log "auto-dismissing prompt (Escape) — matched: $phrase"
      tmux send-keys -t "$SESSION" Escape
      sleep 2
      return 0
    fi
  done <<< "$AUTO_DISMISS_PHRASES"
}

# ─── Notify on phrase ─────────────────────────────────────────────────────

NOTIFY_PHRASE_ALERTED=""

notify_if_phrase_present() {
  [ -z "$NOTIFY_ON_PHRASE_REGEX" ] && return 0
  local topic="${NTFY_TOPIC:-}"
  [ -z "$topic" ] && return 0
  local pane
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0
  if echo "$pane" | grep -qiE "$NOTIFY_ON_PHRASE_REGEX"; then
    if [ -z "$NOTIFY_PHRASE_ALERTED" ]; then
      log "notify-phrase matched; pushing ntfy alert"
      curl -sS -m 10 \
        -H "Priority: high" \
        -H "Title: $NOTIFY_ON_PHRASE_TITLE" \
        -H "Tags: warning,key" \
        -d "$NOTIFY_ON_PHRASE_BODY" \
        "https://ntfy.sh/$topic" >/dev/null || true
      NOTIFY_PHRASE_ALERTED="yes"
    fi
  else
    if [ -n "$NOTIFY_PHRASE_ALERTED" ]; then
      log "notify-phrase cleared"
      curl -sS -m 10 \
        -H "Priority: default" \
        -H "Title: $NOTIFY_ON_PHRASE_TITLE — cleared" \
        -H "Tags: white_check_mark" \
        -d "Phrase no longer visible in pane." \
        "https://ntfy.sh/$topic" >/dev/null || true
      NOTIFY_PHRASE_ALERTED=""
    fi
  fi
}

# ─── Rate-limit failover (opt-in via AGENT_RATE_LIMIT_FAILOVER=true) ──────

check_rate_limit_and_failover() {
  [ "$RATE_LIMIT_FAILOVER" != "true" ] && return 0
  [ -z "$FAILOVER_CMD" ] && return 0

  local pane topic now elapsed
  topic="${NTFY_TOPIC:-}"
  pane=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null) || return 0

  if echo "$pane" | grep -qiE "$RATE_LIMIT_REGEX"; then
    # Pinned agents must not be switched — alert but stay on the pinned CLI
    local envfile="/etc/supervised-agent/${SESSION}.env"
    if grep -q "^AGENT_CLI_PINNED=true" "$envfile" 2>/dev/null; then
      if [ -z "$RATE_LIMIT_ALERTED" ]; then
        log "rate limit on $CLI but $SESSION is PINNED — NOT switching"
        [ -n "$topic" ] && curl -sS -m 10 \
          -H "Priority: high" \
          -H "Title: $SESSION: rate-limited but pinned (staying on $CLI)" \
          -H "Tags: pushpin,warning" \
          -d "$SESSION hit rate limit on $CLI but is pinned — not switching." \
          "https://ntfy.sh/$topic" >/dev/null || true
        RATE_LIMIT_ALERTED="pinned"
      fi
      return 0
    fi
    if [ -z "$RATE_LIMIT_ALERTED" ]; then
      now=$(date +%s)
      elapsed=$((now - LAST_SWITCH_EPOCH))
      if (( elapsed < RATE_LIMIT_COOLDOWN_SEC )); then
        log "rate limit on $CLI but cooldown active (${elapsed}s < ${RATE_LIMIT_COOLDOWN_SEC}s) — NOT switching"
        RATE_LIMIT_ALERTED="cooldown"
        return 0
      fi

      local old_cli="$CLI"
      if [ "$CLI" = "$FAILOVER_CLI" ]; then
        CLI="${AGENT_CLI:-claude}"
        ACTIVE_LAUNCH_CMD="$AGENT_LAUNCH_CMD"
      else
        CLI="$FAILOVER_CLI"
        ACTIVE_LAUNCH_CMD="$FAILOVER_CMD"
      fi
      log "rate limit on $old_cli; switching to $CLI"

      [ -n "$topic" ] && curl -sS -m 10 \
        -H "Priority: high" \
        -H "Title: $SESSION: rate limit on $old_cli → $CLI" \
        -H "Tags: rotating_light,key" \
        -d "$SESSION at $(hostname) hit CLI usage limit on $old_cli. Switching to $CLI." \
        "https://ntfy.sh/$topic" >/dev/null || true

      RATE_LIMIT_ALERTED="yes"
      LAST_SWITCH_EPOCH=$(date +%s)
      start_session
    fi
  else
    if [ -n "$RATE_LIMIT_ALERTED" ]; then
      log "rate limit cleared; $CLI running"
      [ -n "$topic" ] && curl -sS -m 10 \
        -H "Priority: default" \
        -H "Title: $SESSION: resumed on $CLI" \
        -H "Tags: white_check_mark" \
        -d "$SESSION resumed on $CLI." \
        "https://ntfy.sh/$topic" >/dev/null || true
      RATE_LIMIT_ALERTED=""
    fi
  fi
}

# ─── Liveness ──────────────────────────────────────────────────────────────

session_alive() { tmux has-session -t "$SESSION" 2>/dev/null; }

agent_alive() {
  local pids p cmd
  pids=$(tmux list-panes -t "$SESSION" -F "#{pane_pid}" 2>/dev/null) || return 1
  [ -n "$pids" ] || return 1
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

# ─── Main loop ─────────────────────────────────────────────────────────────

trap 'log "supervisor exiting"; exit 0' TERM INT

log "supervisor started (session=$SESSION, cli=$CLI, poll=${POLL_SEC}s, failover=$RATE_LIMIT_FAILOVER)"
start_session
while true; do
  if ! session_alive; then
    log "session $SESSION gone; restarting"
    start_session
  elif ! agent_alive; then
    log "agent process missing in $SESSION; restarting"
    start_session
  else
    handle_startup_dialogs
    check_prompt_delivered
    approve_prompt_if_present
    dismiss_prompts_if_present
    notify_if_phrase_present
    check_rate_limit_and_failover
  fi
  sleep "$POLL_SEC"
done
