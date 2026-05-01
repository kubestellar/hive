#!/bin/bash
# Stall detector + auto-respawn + ntfy push.
#
# Watches AGENT_LOG_FILE's mtime. If the agent hasn't appended in
# AGENT_STALE_MAX_SEC seconds, we consider the agent stalled. We kill the tmux
# session (the supervisor will respawn it within ~10s) and push an alert.
# After AGENT_MAX_RESPAWNS consecutive failed respawns we stop respawning and
# send an escalation alert — manual intervention time.
set -u

: "${AGENT_SESSION_NAME:?AGENT_SESSION_NAME is required}"
: "${AGENT_LOG_FILE:?AGENT_LOG_FILE is required}"

SESSION="$AGENT_SESSION_NAME"
LOG="$AGENT_LOG_FILE"
STATE_DIR="${AGENT_STATE_DIR:-/tmp/hive-healthcheck}"
STALE_FLAG="$STATE_DIR/stale-since"
RESPAWN_COUNT_FILE="$STATE_DIR/respawn-count"
STALE_MAX_SEC="${AGENT_STALE_MAX_SEC:-1800}"
MAX_RESPAWNS="${AGENT_MAX_RESPAWNS:-3}"
NTFY_TOPIC="${NTFY_TOPIC:-}"
NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"
SLACK_WEBHOOK="${SLACK_WEBHOOK:-}"
DISCORD_WEBHOOK="${DISCORD_WEBHOOK:-}"
NOTIFY_LIB="${NOTIFY_LIB:-/usr/local/bin/notify.sh}"
[ -f "$NOTIFY_LIB" ] && . "$NOTIFY_LIB"

_HC_SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
for _cf in "${_HC_SCRIPT_DIR}/hive-config.sh" /usr/local/bin/hive-config.sh; do
  if [[ -f "$_cf" ]]; then source "$_cf"; break; fi
done

if hive_is_paused "$SESSION"; then
  hive_log "$SESSION is paused — skipping healthcheck"
  exit 0
fi

mkdir -p "$STATE_DIR"

notify() {
  # Fallback if notify.sh not installed yet — ntfy only
  local _priority="$1" title="$2" body="$3"
  if [ -z "$NTFY_TOPIC" ]; then
    printf '[%s] (notifications disabled) %s: %s\n' "$(date -Is)" "$title" "$body"
    return 0
  fi
  local pri="default"
  [[ "$_priority" == "urgent" || "$_priority" == "high" ]] && pri="urgent"
  curl -sS -m 10 \
    -H "Priority: $pri" \
    -H "Title: $title" \
    -d "$body" \
    "$NTFY_SERVER/$NTFY_TOPIC" >/dev/null || true
}

respawn() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
}

now=$(date +%s)
log_mtime=$(stat -c %Y "$LOG" 2>/dev/null || echo 0)
age=$((now - log_mtime))
age_min=$((age / 60))

if [ "$age" -gt "$STALE_MAX_SEC" ]; then
  # Stalled.
  count=$(cat "$RESPAWN_COUNT_FILE" 2>/dev/null || echo 0)

  if [ ! -f "$STALE_FLAG" ]; then
    # First detection of this stall event.
    echo "$now" > "$STALE_FLAG"
    count=0
  fi

  if [ "$count" -ge "$MAX_RESPAWNS" ]; then
    # Too many failed respawns. Alert once on the transition to "giving up",
    # then stay silent until recovery.
    if [ "$count" -eq "$MAX_RESPAWNS" ]; then
      notify urgent \
        "Supervised agent stalled — manual intervention needed" \
        "Log $LOG is ${age_min}m old. ${MAX_RESPAWNS} respawn attempts failed. Check: journalctl -u hive -n 50" \
        "fire,rotating_light"
      echo $((count + 1)) > "$RESPAWN_COUNT_FILE"
    fi
    exit 0
  fi

  count=$((count + 1))
  echo "$count" > "$RESPAWN_COUNT_FILE"
  respawn
  notify high \
    "Supervised agent stalled — respawning" \
    "Log $LOG is ${age_min}m old. Respawn attempt ${count}/${MAX_RESPAWNS}. Supervisor will rebuild the session in ~$((10))s." \
    "warning,arrows_counterclockwise"
else
  # Fresh.
  if [ -f "$STALE_FLAG" ]; then
    notify default \
      "Supervised agent recovered" \
      "Latest heartbeat ${age_min}m ago. Clearing stall state." \
      "white_check_mark"
    rm -f "$STALE_FLAG" "$RESPAWN_COUNT_FILE"
  fi
fi
