#!/bin/bash
# kick-governor.sh — Adaptive kick governor for supervised agents.
#
# Runs every 15 minutes via kick-governor.timer. Queries the 5 KubeStellar
# Console repos, measures the actionable issue+PR backlog, then kicks each
# agent at a cadence that reflects the current workload:
#
#   BUSY (queue > BUSY_THRESHOLD_ISSUES):
#     scanner   → every 15 min  (always the minimum)
#     reviewer  → every 15 min
#     architect → every 3 hours
#     outreach  → every 3 hours
#
#   QUIET (queue ≤ BUSY_THRESHOLD_ISSUES):
#     scanner   → every 15 min
#     reviewer  → every 30 min
#     architect → every 1 hour
#     outreach  → every 1 hour
#
# State lives in STATE_DIR (tmpfs — cleared on reboot, fine for kick timing).
# Logs go to journald via stdout + LOG_FILE for human review.

set -euo pipefail

# ── Repos to scan ───────────────────────────────────────────────────────────
REPOS=(
  kubestellar/console
  kubestellar/console-kb
  kubestellar/docs
  kubestellar/console-marketplace
  kubestellar/kubestellar-mcp
)

# ── Exempt-label filter ─────────────────────────────────────────────────────
# Issues matching any of these labels are NOT counted toward the actionable queue.
EXEMPT_LABEL_REGEX="nightly-tests|LFX|do-not-merge|meta-tracker|auto-qa-tuning-report|hold|adopters"

# ── Queue depth threshold ───────────────────────────────────────────────────
# Above this → BUSY mode; at or below → QUIET mode.
BUSY_THRESHOLD_ISSUES=10

# ── Kick cadences (seconds) ─────────────────────────────────────────────────
CADENCE_SCANNER_SEC=900           # 15 min — never changes regardless of mode
CADENCE_REVIEWER_BUSY_SEC=900     # 15 min in busy mode
CADENCE_REVIEWER_QUIET_SEC=1800   # 30 min in quiet mode
CADENCE_SLOW_BUSY_SEC=10800       # 3 hours in busy mode  (architect + outreach)
CADENCE_SLOW_QUIET_SEC=3600       # 1 hour  in quiet mode (architect + outreach)

# ── Paths ───────────────────────────────────────────────────────────────────
STATE_DIR="/var/run/kick-governor"
LOG_FILE="/var/log/kick-governor.log"
KICK_SCRIPT="${KICK_SCRIPT:-/usr/local/bin/kick-agents.sh}"
GH_BIN="${GH_BIN:-gh}"

# ── Notification config (optional) ─────────────────────────────────────────
NTFY_TOPIC="${NTFY_TOPIC:-}"
NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"

# ── Log rotation ────────────────────────────────────────────────────────────
MAX_LOG_LINES=500

# ── Helpers ─────────────────────────────────────────────────────────────────
TIMESTAMP="$(TZ=America/New_York date '+%Y-%m-%d %H:%M:%S %Z')"

log() {
  local msg="[$TIMESTAMP] $*"
  echo "$msg"
  echo "$msg" >> "$LOG_FILE" 2>/dev/null || true
}

ntfy() {
  [ -z "$NTFY_TOPIC" ] && return 0
  local priority="$1" title="$2" body="$3" tags="${4:-}"
  curl -sS -m 10 \
    -H "Priority: $priority" \
    -H "Title: $title" \
    -H "Tags: $tags" \
    -d "$body" \
    "$NTFY_SERVER/$NTFY_TOPIC" >/dev/null 2>&1 || true
}

rotate_log() {
  [ ! -f "$LOG_FILE" ] && return 0
  local lines
  lines=$(wc -l < "$LOG_FILE" 2>/dev/null || echo 0)
  if [ "$lines" -gt "$MAX_LOG_LINES" ]; then
    tail -"$((MAX_LOG_LINES / 2))" "$LOG_FILE" > "${LOG_FILE}.tmp" \
      && mv "${LOG_FILE}.tmp" "$LOG_FILE" || true
  fi
}

secs_to_label() {
  local s="$1"
  if [ "$s" -ge 3600 ]; then
    echo "$((s / 3600))h"
  else
    echo "$((s / 60))min"
  fi
}

# ── Queue depth measurement ──────────────────────────────────────────────────
# Counts open issues that are not exempt from the actionable queue.

count_actionable_issues() {
  local repo="$1"
  unset GITHUB_TOKEN
  $GH_BIN issue list \
    --repo "$repo" \
    --state open \
    --json number,labels \
    --limit 200 \
    2>/dev/null \
  | jq --arg rx "$EXEMPT_LABEL_REGEX" \
    '[.[] | select(.labels | map(.name) | any(test($rx; "i")) | not)] | length' \
    2>/dev/null \
  || echo 0
}

measure_queue() {
  local total=0
  local breakdown=""
  for repo in "${REPOS[@]}"; do
    local n
    n=$(count_actionable_issues "$repo")
    total=$((total + n))
    breakdown="${breakdown} ${repo##*/}=${n}"
  done
  echo "$total" > "$STATE_DIR/queue_depth"
  log "QUEUE total=${total} |${breakdown# }"
  echo "$total"
}

get_queue_depth() {
  local depth
  if depth=$(measure_queue 2>&1); then
    echo "$depth"
  else
    # gh failed (rate limit, network); fall back to last known depth
    depth=$(cat "$STATE_DIR/queue_depth" 2>/dev/null || echo "$((BUSY_THRESHOLD_ISSUES + 1))")
    log "QUEUE measure failed — using cached depth=${depth}"
    echo "$depth"
  fi
}

# ── Mode determination ───────────────────────────────────────────────────────

determine_mode() {
  local depth="$1"
  if [ "$depth" -gt "$BUSY_THRESHOLD_ISSUES" ]; then
    echo "busy"
  else
    echo "quiet"
  fi
}

# ── Cadence selection ────────────────────────────────────────────────────────

get_cadence() {
  local agent="$1" mode="$2"
  case "$agent" in
    scanner)
      echo "$CADENCE_SCANNER_SEC"
      ;;
    reviewer)
      [ "$mode" = "busy" ] \
        && echo "$CADENCE_REVIEWER_BUSY_SEC" \
        || echo "$CADENCE_REVIEWER_QUIET_SEC"
      ;;
    architect|outreach)
      [ "$mode" = "busy" ] \
        && echo "$CADENCE_SLOW_BUSY_SEC" \
        || echo "$CADENCE_SLOW_QUIET_SEC"
      ;;
    *)
      echo "$CADENCE_SLOW_QUIET_SEC"
      ;;
  esac
}

# ── Last-kick tracking ───────────────────────────────────────────────────────

last_kick_file() { echo "$STATE_DIR/last_kick_${1}"; }

seconds_since_last_kick() {
  local f
  f=$(last_kick_file "$1")
  if [ ! -f "$f" ]; then
    echo 999999  # never kicked — always fire on first governor run
    return
  fi
  local last now
  last=$(cat "$f")
  now=$(date +%s)
  echo $((now - last))
}

record_kick() {
  date +%s > "$(last_kick_file "$1")"
}

# ── Per-agent kick dispatch ───────────────────────────────────────────────────

maybe_kick() {
  local agent="$1" mode="$2"
  local cadence elapsed
  cadence=$(get_cadence "$agent" "$mode")
  elapsed=$(seconds_since_last_kick "$agent")

  if [ "$elapsed" -ge "$cadence" ]; then
    log "KICK ${agent} (mode=${mode} cadence=$(secs_to_label "$cadence") elapsed=$(secs_to_label "$elapsed"))"
    if "$KICK_SCRIPT" "$agent" 2>&1 \
        | while IFS= read -r line; do log "  [${agent}] ${line}"; done; then
      record_kick "$agent"
    else
      log "  [${agent}] kick-agents.sh exited non-zero — not recording kick time (will retry next tick)"
    fi
  else
    local remaining=$(( cadence - elapsed ))
    log "SKIP ${agent} (mode=${mode} next in $(secs_to_label "$remaining"))"
  fi
}

# ── Main ─────────────────────────────────────────────────────────────────────

mkdir -p "$STATE_DIR"
rotate_log
log "GOVERNOR START"

queue_depth=$(get_queue_depth)
mode=$(determine_mode "$queue_depth")

prev_mode=$(cat "$STATE_DIR/mode" 2>/dev/null || echo "")
echo "$mode" > "$STATE_DIR/mode"

# Report mode transitions
if [ -n "$prev_mode" ] && [ "$prev_mode" != "$mode" ]; then
  log "MODE CHANGE ${prev_mode} → ${mode} (queue=${queue_depth} threshold=${BUSY_THRESHOLD_ISSUES})"
  ntfy "default" \
    "Governor: ${prev_mode} → ${mode}" \
    "Queue depth ${queue_depth} (threshold ${BUSY_THRESHOLD_ISSUES}). Cadences: scanner=$(secs_to_label "$CADENCE_SCANNER_SEC") reviewer=$(secs_to_label "$(get_cadence reviewer "$mode")") architect=$(secs_to_label "$(get_cadence architect "$mode")") outreach=$(secs_to_label "$(get_cadence outreach "$mode")")" \
    "arrows_counterclockwise"
fi

log "MODE=${mode} queue=${queue_depth} scanner=$(secs_to_label "$(get_cadence scanner "$mode")") reviewer=$(secs_to_label "$(get_cadence reviewer "$mode")") architect=$(secs_to_label "$(get_cadence architect "$mode")") outreach=$(secs_to_label "$(get_cadence outreach "$mode")")"

maybe_kick scanner   "$mode"
maybe_kick reviewer  "$mode"
maybe_kick architect "$mode"
maybe_kick outreach  "$mode"

log "GOVERNOR DONE"
