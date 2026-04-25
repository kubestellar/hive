#!/bin/bash
# kick-governor.sh — Adaptive kick governor for supervised agents.
#
# Runs every 15 minutes via kick-governor.timer. Queries the 5 KubeStellar
# Console repos, measures the actionable issue+PR backlog, then kicks each
# agent at a cadence that reflects the current workload:
#
# Architect and outreach are OPPORTUNISTIC — they fill idle cycles and yield
# entirely under load. Scanner and reviewer always have priority.
#
#   SURGE (queue > SURGE_THRESHOLD, default 20):
#     scanner   → every 15 min
#     reviewer  → every 30 min
#     architect → PAUSED
#     outreach  → every 30 min
#
#   BUSY (queue > BUSY_THRESHOLD, default 10):
#     scanner   → every 15 min
#     reviewer  → every 15 min
#     architect → every 1 hour
#     outreach  → every 1 hour
#
#   QUIET (queue > IDLE_THRESHOLD, default 2):
#     scanner   → every 15 min
#     reviewer  → every 30 min
#     architect → every 30 min
#     outreach  → every 2 hours
#
#   IDLE (queue ≤ IDLE_THRESHOLD):
#     scanner   → every 30 min
#     reviewer  → every 1 hour
#     architect → every 30 min  (jam — queue is clear)
#     outreach  → every 2 hours
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
EXEMPT_LABEL_REGEX="nightly-tests|LFX|do-not-merge|meta-tracker|auto-qa-tuning-report|hold|adopters|changes-requested|waiting-on-author"

# ── Queue depth thresholds ──────────────────────────────────────────────────
# SURGE → BUSY → QUIET → IDLE as queue drains.
SURGE_THRESHOLD_ISSUES="${SURGE_THRESHOLD_ISSUES:-20}"
BUSY_THRESHOLD_ISSUES="${BUSY_THRESHOLD_ISSUES:-10}"
IDLE_THRESHOLD_ISSUES="${IDLE_THRESHOLD_ISSUES:-2}"

# ── Kick cadences (seconds) ─────────────────────────────────────────────────
# All overridable via /etc/hive/governor.env — no script edit needed.
# 0 = PAUSED (agent is not kicked in this mode).
CADENCE_SCANNER_SURGE_SEC="${CADENCE_SCANNER_SURGE_SEC:-900}"     # 15 min
CADENCE_SCANNER_BUSY_SEC="${CADENCE_SCANNER_BUSY_SEC:-900}"       # 15 min
CADENCE_SCANNER_QUIET_SEC="${CADENCE_SCANNER_QUIET_SEC:-900}"     # 15 min
CADENCE_SCANNER_IDLE_SEC="${CADENCE_SCANNER_IDLE_SEC:-1800}"      # 30 min

CADENCE_REVIEWER_SURGE_SEC="${CADENCE_REVIEWER_SURGE_SEC:-1800}"   # 30 min
CADENCE_REVIEWER_BUSY_SEC="${CADENCE_REVIEWER_BUSY_SEC:-900}"     # 15 min
CADENCE_REVIEWER_QUIET_SEC="${CADENCE_REVIEWER_QUIET_SEC:-1800}"  # 30 min
CADENCE_REVIEWER_IDLE_SEC="${CADENCE_REVIEWER_IDLE_SEC:-3600}"    # 1 hour

CADENCE_ARCHITECT_SURGE_SEC="${CADENCE_ARCHITECT_SURGE_SEC:-0}"     # PAUSED
CADENCE_ARCHITECT_BUSY_SEC="${CADENCE_ARCHITECT_BUSY_SEC:-3600}"    # 1 hour
CADENCE_ARCHITECT_QUIET_SEC="${CADENCE_ARCHITECT_QUIET_SEC:-1800}"  # 30 min
CADENCE_ARCHITECT_IDLE_SEC="${CADENCE_ARCHITECT_IDLE_SEC:-1800}"    # 30 min (jam)

CADENCE_SUPERVISOR_SURGE_SEC="${CADENCE_SUPERVISOR_SURGE_SEC:-300}"   # 5 min
CADENCE_SUPERVISOR_BUSY_SEC="${CADENCE_SUPERVISOR_BUSY_SEC:-600}"    # 10 min
CADENCE_SUPERVISOR_QUIET_SEC="${CADENCE_SUPERVISOR_QUIET_SEC:-900}"  # 15 min
CADENCE_SUPERVISOR_IDLE_SEC="${CADENCE_SUPERVISOR_IDLE_SEC:-1800}"   # 30 min

CADENCE_OUTREACH_SURGE_SEC="${CADENCE_OUTREACH_SURGE_SEC:-1800}"    # 30 min
CADENCE_OUTREACH_BUSY_SEC="${CADENCE_OUTREACH_BUSY_SEC:-3600}"      # 1 hour
CADENCE_OUTREACH_QUIET_SEC="${CADENCE_OUTREACH_QUIET_SEC:-7200}"    # 2 hours
CADENCE_OUTREACH_IDLE_SEC="${CADENCE_OUTREACH_IDLE_SEC:-7200}"      # 2 hours

# ── Token budget ────────────────────────────────────────────────────────────
TOKEN_BUDGET_WEEKLY="${TOKEN_BUDGET_WEEKLY:-50000000}"
TOKEN_BUDGET_SAFETY_PCT="${TOKEN_BUDGET_SAFETY_PCT:-85}"
TOKEN_BUDGET_RESET_DAY="${TOKEN_BUDGET_RESET_DAY:-0}"  # 0=Sunday
TOKEN_COLLECTOR_JSON="/var/run/hive-metrics/tokens.json"

# ── Cost weights (relative to Haiku=1) ──────────────────────────────────────
COST_WEIGHT_OPUS="${COST_WEIGHT_OPUS:-15}"
COST_WEIGHT_SONNET="${COST_WEIGHT_SONNET:-3}"
COST_WEIGHT_HAIKU="${COST_WEIGHT_HAIKU:-1}"

# ── Model selection table: MODEL_<MODE>_<AGENT>=backend:model ───────────────
# Priority agents (scanner, reviewer) get metered Claude in surge/busy.
# Non-priority agents (architect, outreach) use copilot (free/unlimited).
# Supervisor is lightweight — Haiku or copilot.
MODEL_SURGE_SCANNER="${MODEL_SURGE_SCANNER:-claude:claude-sonnet-4-6}"
MODEL_SURGE_REVIEWER="${MODEL_SURGE_REVIEWER:-claude:claude-sonnet-4-6}"
MODEL_SURGE_ARCHITECT="${MODEL_SURGE_ARCHITECT:-claude:claude-opus-4-6}"
MODEL_SURGE_OUTREACH="${MODEL_SURGE_OUTREACH:-copilot:claude-opus-4.6}"
MODEL_SURGE_SUPERVISOR="${MODEL_SURGE_SUPERVISOR:-claude:claude-haiku-4-5}"

MODEL_BUSY_SCANNER="${MODEL_BUSY_SCANNER:-claude:claude-sonnet-4-6}"
MODEL_BUSY_REVIEWER="${MODEL_BUSY_REVIEWER:-claude:claude-sonnet-4-6}"
MODEL_BUSY_ARCHITECT="${MODEL_BUSY_ARCHITECT:-copilot:claude-sonnet-4.6}"
MODEL_BUSY_OUTREACH="${MODEL_BUSY_OUTREACH:-copilot:claude-sonnet-4.6}"
MODEL_BUSY_SUPERVISOR="${MODEL_BUSY_SUPERVISOR:-claude:claude-haiku-4-5}"

MODEL_QUIET_SCANNER="${MODEL_QUIET_SCANNER:-claude:claude-haiku-4-5}"
MODEL_QUIET_REVIEWER="${MODEL_QUIET_REVIEWER:-copilot:claude-sonnet-4.6}"
MODEL_QUIET_ARCHITECT="${MODEL_QUIET_ARCHITECT:-copilot:claude-opus-4.6}"
MODEL_QUIET_OUTREACH="${MODEL_QUIET_OUTREACH:-copilot:claude-sonnet-4.6}"
MODEL_QUIET_SUPERVISOR="${MODEL_QUIET_SUPERVISOR:-claude:claude-haiku-4-5}"

MODEL_IDLE_SCANNER="${MODEL_IDLE_SCANNER:-copilot:claude-sonnet-4.6}"
MODEL_IDLE_REVIEWER="${MODEL_IDLE_REVIEWER:-copilot:claude-sonnet-4.6}"
MODEL_IDLE_ARCHITECT="${MODEL_IDLE_ARCHITECT:-copilot:claude-opus-4.6}"
MODEL_IDLE_OUTREACH="${MODEL_IDLE_OUTREACH:-copilot:claude-sonnet-4.6}"
MODEL_IDLE_SUPERVISOR="${MODEL_IDLE_SUPERVISOR:-copilot:claude-sonnet-4.6}"

RATE_LIMIT_FALLBACK_BACKEND="${RATE_LIMIT_FALLBACK_BACKEND:-copilot}"
RATE_LIMIT_COOLDOWN="${RATE_LIMIT_COOLDOWN:-1800}"  # 30 min

# ── Paths ───────────────────────────────────────────────────────────────────
STATE_DIR="/var/run/kick-governor"
LOG_FILE="/var/log/kick-governor.log"
KICK_SCRIPT="${KICK_SCRIPT:-/usr/local/bin/kick-agents.sh}"
GH_BIN="${GH_BIN:-gh}"

# ── Notification config ─────────────────────────────────────────────────────
NTFY_TOPIC="${NTFY_TOPIC:-}"
NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"
SLACK_WEBHOOK="${SLACK_WEBHOOK:-}"
DISCORD_WEBHOOK="${DISCORD_WEBHOOK:-}"
# shellcheck source=notify.sh
NOTIFY_LIB="${NOTIFY_LIB:-/usr/local/bin/notify.sh}"
[ -f "$NOTIFY_LIB" ] && . "$NOTIFY_LIB"

# ── Log rotation ────────────────────────────────────────────────────────────
MAX_LOG_LINES=500

# ── Helpers ─────────────────────────────────────────────────────────────────
TIMESTAMP="$(TZ=America/New_York date '+%Y-%m-%d %H:%M:%S %Z')"

log() {
  local msg="[$TIMESTAMP] $*"
  echo "$msg" >&2
  echo "$msg" >> "$LOG_FILE" 2>/dev/null || true
}

ntfy() {
  # Legacy wrapper — maps old 4-arg ntfy() calls to notify()
  # ntfy <priority> <title> <body> <tags>
  local _priority="$1" title="$2" body="$3"
  local pri="default"
  [[ "$_priority" == "urgent" || "$_priority" == "high" ]] && pri="high"
  [[ "$_priority" == "low" || "$_priority" == "min" ]]    && pri="low"
  notify "$title" "$body" "$pri"
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
# Uses REST API (separate 5000 req/hr pool) instead of GraphQL to avoid rate limits.

count_actionable() {
  local repo="$1"
  local owner="${repo%%/*}"
  local name="${repo##*/}"
  local cache_dir="$STATE_DIR/repo_cache"
  mkdir -p "$cache_dir" 2>/dev/null || true
  unset GITHUB_TOKEN
  local issues prs

  # REST API: list open issues (excludes PRs), filter out exempt labels client-side
  local issues_json
  issues_json=$($GH_BIN api "repos/${repo}/issues?state=open&per_page=100" --paginate 2>/dev/null)
  if [ -n "$issues_json" ]; then
    issues=$(echo "$issues_json" | jq --arg rx "$EXEMPT_LABEL_REGEX" \
      '[.[] | select(.pull_request == null) | select(.labels | map(.name) | any(test($rx; "i")) | not)] | length' 2>/dev/null || echo "-1")
  else
    issues="-1"
  fi

  # REST API: list open PRs, filter out exempt labels
  local prs_json
  prs_json=$($GH_BIN api "repos/${repo}/pulls?state=open&per_page=100" --paginate 2>/dev/null)
  if [ -n "$prs_json" ]; then
    prs=$(echo "$prs_json" | jq --arg rx "$EXEMPT_LABEL_REGEX" \
      '[.[] | select(.labels | map(.name) | any(test($rx; "i")) | not)] | length' 2>/dev/null || echo "-1")
  else
    prs="-1"
  fi

  # Fall back to cached values when API fails
  [[ "$issues" == "-1" ]] && issues=$(cat "$cache_dir/${name}_actionable_issues" 2>/dev/null || echo "0")
  [[ "$prs"    == "-1" ]] && prs=$(   cat "$cache_dir/${name}_actionable_prs"    2>/dev/null || echo "0")

  # Cache successful results
  [[ "$issues" != "-1" ]] && echo "$issues" > "$cache_dir/${name}_actionable_issues"
  [[ "$prs"    != "-1" ]] && echo "$prs"    > "$cache_dir/${name}_actionable_prs"

  echo "${issues} ${prs}"
}

measure_queue() {
  local total=0 total_i=0 total_p=0
  local breakdown=""
  for repo in "${REPOS[@]}"; do
    local counts i p
    counts=$(count_actionable "$repo")
    i="${counts%% *}"
    p="${counts##* }"
    total_i=$(( total_i + i ))
    total_p=$(( total_p + p ))
    total=$(( total_i + total_p ))
    breakdown="${breakdown} ${repo##*/}=${i}i/${p}p"
  done
  echo "$total"        > "$STATE_DIR/queue_depth"
  echo "$total_i"      > "$STATE_DIR/queue_issues"
  echo "$total_p"      > "$STATE_DIR/queue_prs"
  log "QUEUE total=${total} (${total_i}i/${total_p}p) |${breakdown# }"
  echo "$total"
}

get_queue_depth() {
  local depth
  if depth=$(measure_queue); then
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
  if [ "$depth" -gt "$SURGE_THRESHOLD_ISSUES" ]; then
    echo "surge"
  elif [ "$depth" -gt "$BUSY_THRESHOLD_ISSUES" ]; then
    echo "busy"
  elif [ "$depth" -gt "$IDLE_THRESHOLD_ISSUES" ]; then
    echo "quiet"
  else
    echo "idle"
  fi
}

# ── Cadence selection ────────────────────────────────────────────────────────

get_cadence() {
  local agent="$1" mode="$2"
  case "$agent" in
    scanner)
      case "$mode" in
        surge) echo "$CADENCE_SCANNER_SURGE_SEC" ;;
        busy)  echo "$CADENCE_SCANNER_BUSY_SEC"  ;;
        quiet) echo "$CADENCE_SCANNER_QUIET_SEC" ;;
        idle)  echo "$CADENCE_SCANNER_IDLE_SEC"  ;;
      esac ;;
    reviewer)
      case "$mode" in
        surge) echo "$CADENCE_REVIEWER_SURGE_SEC" ;;
        busy)  echo "$CADENCE_REVIEWER_BUSY_SEC"  ;;
        quiet) echo "$CADENCE_REVIEWER_QUIET_SEC" ;;
        idle)  echo "$CADENCE_REVIEWER_IDLE_SEC"  ;;
      esac ;;
    architect)
      case "$mode" in
        surge) echo "$CADENCE_ARCHITECT_SURGE_SEC" ;;
        busy)  echo "$CADENCE_ARCHITECT_BUSY_SEC"  ;;
        quiet) echo "$CADENCE_ARCHITECT_QUIET_SEC" ;;
        idle)  echo "$CADENCE_ARCHITECT_IDLE_SEC"  ;;
      esac ;;
    outreach)
      case "$mode" in
        surge) echo "$CADENCE_OUTREACH_SURGE_SEC" ;;
        busy)  echo "$CADENCE_OUTREACH_BUSY_SEC"  ;;
        quiet) echo "$CADENCE_OUTREACH_QUIET_SEC" ;;
        idle)  echo "$CADENCE_OUTREACH_IDLE_SEC"  ;;
      esac ;;
    supervisor)
      case "$mode" in
        surge) echo "$CADENCE_SUPERVISOR_SURGE_SEC" ;;
        busy)  echo "$CADENCE_SUPERVISOR_BUSY_SEC"  ;;
        quiet) echo "$CADENCE_SUPERVISOR_QUIET_SEC" ;;
        idle)  echo "$CADENCE_SUPERVISOR_IDLE_SEC"  ;;
      esac ;;
    *)
  esac
}

# ── Model selection ──────────────────────────────────────────────────────────

convert_model_notation() {
  local model="$1" target_backend="$2"
  case "$target_backend" in
    copilot) echo "$model" | sed -E 's/([0-9])-([0-9])/\1.\2/g' ;;
    *)       echo "$model" | sed -E 's/([0-9])\.([0-9])/\1-\2/g' ;;
  esac
}

get_model_selection() {
  local agent="$1" mode="$2"
  local upper_mode upper_agent
  upper_mode=$(echo "$mode" | tr '[:lower:]' '[:upper:]')
  upper_agent=$(echo "$agent" | tr '[:lower:]' '[:upper:]')
  local var_name="MODEL_${upper_mode}_${upper_agent}"
  local selection="${!var_name}"
  if [[ -z "$selection" ]]; then
    selection="copilot:claude-sonnet-4.6"
  fi
  echo "$selection"
}

get_cost_weight() {
  local backend="$1" model="$2"
  case "$backend" in
    copilot|goose) echo 0; return ;;
  esac
  case "$model" in
    *opus*)   echo "${COST_WEIGHT_OPUS}" ;;
    *sonnet*) echo "${COST_WEIGHT_SONNET}" ;;
    *haiku*)  echo "${COST_WEIGHT_HAIKU}" ;;
    *)        echo "${COST_WEIGHT_SONNET}" ;;
  esac
}

is_rate_limited() {
  local backend="$1"
  local rl_file="$STATE_DIR/rate_limits"
  [[ ! -f "$rl_file" ]] && return 1
  local now
  now=$(date +%s)
  while IFS=: read -r rl_backend rl_time _rl_agent; do
    if [[ "$rl_backend" == "$backend" ]] && (( now - rl_time < RATE_LIMIT_COOLDOWN )); then
      return 0
    fi
  done < "$rl_file"
  return 1
}

compute_budget_state() {
  local budget_file="$STATE_DIR/budget_state"
  local used=0 burn_hourly=0

  if [[ -f "$TOKEN_COLLECTOR_JSON" ]]; then
    # Count only billable tokens (input + output). Cache read is free/discounted.
    read -r used burn_hourly <<< "$(python3 -c "
import json
try:
    with open('$TOKEN_COLLECTOR_JSON') as f:
        d = json.load(f)
    weekly = d.get('weekly', {})
    used = weekly.get('billableTokens', 0)
    if used == 0:
        wt = weekly.get('totals', {})
        used = wt.get('input', 0) + wt.get('output', 0)
    hourly = d.get('hourlyBurnRate', {}).get('billable', 0)
    if hourly == 0:
        hb = d.get('hourlyBurnRate', {})
        total_hr = hb.get('total', 0)
        wt = weekly.get('totals', {})
        wall = wt.get('input', 0) + wt.get('output', 0) + wt.get('cacheRead', 0)
        ratio = (wt.get('input', 0) + wt.get('output', 0)) / wall if wall > 0 else 0.01
        hourly = int(total_hr * ratio)
    print(used, hourly)
except Exception:
    print(0, 0)
" 2>/dev/null || echo "0 0")"
  fi

  local remaining=$((TOKEN_BUDGET_WEEKLY - used))
  [[ "$remaining" -lt 0 ]] && remaining=0
  local pct_used=0
  [[ "$TOKEN_BUDGET_WEEKLY" -gt 0 ]] && pct_used=$((used * 100 / TOKEN_BUDGET_WEEKLY))

  local hours_left
  hours_left=$(python3 -c "
import datetime
now = datetime.datetime.now()
reset_day = $TOKEN_BUDGET_RESET_DAY
days_ahead = (reset_day - now.weekday()) % 7
if days_ahead == 0 and now.hour > 0: days_ahead = 7
reset = (now + datetime.timedelta(days=days_ahead)).replace(hour=0, minute=0, second=0, microsecond=0)
print(max(1, int((reset - now).total_seconds() // 3600)))
" 2>/dev/null || echo 168)

  local projected=$((used + burn_hourly * hours_left))
  local projected_pct=0
  [[ "$TOKEN_BUDGET_WEEKLY" -gt 0 ]] && projected_pct=$((projected * 100 / TOKEN_BUDGET_WEEKLY))

  cat > "$budget_file" <<BUDGETEOF
BUDGET_WEEKLY=$TOKEN_BUDGET_WEEKLY
BUDGET_USED=$used
BUDGET_REMAINING=$remaining
BUDGET_PCT_USED=$pct_used
BURN_RATE_HOURLY=$burn_hourly
HOURS_REMAINING=$hours_left
PROJECTED_WEEKLY=$projected
PROJECTED_PCT=$projected_pct
LAST_UPDATED=$(date -Iseconds)
BUDGETEOF

  echo "$projected_pct"
}

optimize_model_assignment() {
  local mode="$1"
  local agents=(scanner reviewer architect outreach supervisor)
  local priority_agents=(scanner reviewer)

  local projected_pct
  projected_pct=$(compute_budget_state)

  declare -A assignments
  declare -A override_reasons
  for agent in "${agents[@]}"; do
    assignments[$agent]=$(get_model_selection "$agent" "$mode")
    override_reasons[$agent]=""
  done

  if (( projected_pct > TOKEN_BUDGET_SAFETY_PCT )); then
    log "BUDGET PRESSURE: projected ${projected_pct}% > safety ${TOKEN_BUDGET_SAFETY_PCT}%"

    for agent in outreach architect supervisor; do
      local current="${assignments[$agent]}"
      local backend="${current%%:*}"
      if [[ "$backend" != "copilot" && "$backend" != "goose" ]]; then
        local model="${current#*:}"
        local copilot_model
        copilot_model=$(convert_model_notation "$model" "copilot")
        assignments[$agent]="copilot:${copilot_model}"
        override_reasons[$agent]="budget_downgrade"
        log "  budget override: $agent -> copilot (was $backend)"
      fi
    done

    if (( projected_pct > 95 )); then
      for agent in "${priority_agents[@]}"; do
        local current="${assignments[$agent]}"
        local backend="${current%%:*}"
        local model="${current#*:}"
        case "$model" in
          *opus*)
            local new_model="${model/opus/sonnet}"
            assignments[$agent]="${backend}:${new_model}"
            override_reasons[$agent]="budget_downgrade"
            log "  budget override: $agent opus->sonnet"
            ;;
          *sonnet*)
            local new_model="${model/sonnet/haiku}"
            assignments[$agent]="${backend}:${new_model}"
            override_reasons[$agent]="budget_downgrade"
            log "  budget override: $agent sonnet->haiku"
            ;;
        esac
      done
    fi

    if (( projected_pct > 99 )); then
      for agent in "${agents[@]}"; do
        local current="${assignments[$agent]}"
        local model="${current#*:}"
        local copilot_model
        copilot_model=$(convert_model_notation "$model" "copilot")
        assignments[$agent]="copilot:${copilot_model}"
        override_reasons[$agent]="budget_critical"
      done
      log "  BUDGET CRITICAL: all agents -> copilot"
    fi
  fi

  for agent in "${agents[@]}"; do
    local selection="${assignments[$agent]}"
    local backend="${selection%%:*}"
    local model="${selection#*:}"

    if is_rate_limited "$backend"; then
      local fallback="${RATE_LIMIT_FALLBACK_BACKEND}"
      local fb_model
      fb_model=$(convert_model_notation "$model" "$fallback")
      backend="$fallback"
      model="$fb_model"
      log "  rate-limit swap: $agent -> $fallback"
    fi

    local lock_file="$STATE_DIR/model_lock_${agent}"
    if [[ -f "$lock_file" ]]; then
      log "  LOCKED: $agent (manual override — skipping)"
      continue
    fi

    local cost_weight
    cost_weight=$(get_cost_weight "$backend" "$model")
    local prev_backend prev_model
    prev_backend=$(grep '^BACKEND=' "$STATE_DIR/model_${agent}" 2>/dev/null | cut -d= -f2 || true)
    prev_model=$(grep '^MODEL=' "$STATE_DIR/model_${agent}" 2>/dev/null | cut -d= -f2 || true)

    cat > "$STATE_DIR/model_${agent}" <<MODELEOF
BACKEND=$backend
MODEL=$model
COST_WEIGHT=$cost_weight
REASON=${override_reasons[$agent]:-${mode}_mode}
PREV_BACKEND=${prev_backend:-}
PREV_MODEL=${prev_model:-}
UPDATED=$(date -Iseconds)
MODELEOF

    if [[ -n "$prev_backend" && ("$prev_backend" != "$backend" || "$prev_model" != "$model") ]]; then
      log "MODEL CHANGE ${agent}: ${prev_backend}:${prev_model} -> ${backend}:${model}"
    fi
  done
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

  if [ "$cadence" -eq 0 ]; then
    log "SKIP ${agent} (mode=${mode} — PAUSED)"
    return
  fi

  # Agents kick on quarter-hour boundaries (:00, :15, :30, :45)
  local now_min
  now_min=$(date +%-M)
  local quarter=$(( (now_min / 15) * 15 ))  # 0, 15, 30, or 45
  local on_quarter=false
  [[ $(( now_min - quarter )) -le 4 ]] && on_quarter=true  # within 5-min governor window

  if [ "$elapsed" -ge "$cadence" ] && [ "$on_quarter" = true ]; then
    local next_et
    next_et=$(TZ=America/New_York date -d "+${cadence} seconds" '+%H:%M %Z')
    log "KICK ${agent} (mode=${mode} cadence=$(secs_to_label "$cadence") elapsed=$(secs_to_label "$elapsed") next≈${next_et})"
    ntfy "default" \
      "Kick: ${agent}" \
      "mode=${mode} cadence=$(secs_to_label "$cadence") next≈${next_et} queue=${queue_depth}" \
      "bell"
    if "$KICK_SCRIPT" "$agent" 2>&1 \
        | while IFS= read -r line; do log "  [${agent}] ${line}"; done; then
      record_kick "$agent"
    else
      log "  [${agent}] kick-agents.sh exited non-zero — not recording kick time (will retry next tick)"
    fi
  else
    local remaining=$(( cadence - elapsed ))
    [[ "$remaining" -lt 0 ]] && remaining=0
    if [ "$on_quarter" != true ] && [ "$elapsed" -ge "$cadence" ]; then
      # Overdue but not on quarter — find next quarter
      local mins_to_quarter=$(( 15 - (now_min % 15) ))
      log "SKIP ${agent} (mode=${mode} due — waiting for :${quarter} boundary, ~${mins_to_quarter}m)"
    else
      log "SKIP ${agent} (mode=${mode} next in $(secs_to_label "$remaining"))"
    fi
  fi
}

# ── Main ─────────────────────────────────────────────────────────────────────

mkdir -p "$STATE_DIR"
rotate_log
log "GOVERNOR START"

queue_depth=$(get_queue_depth)
mode=$(determine_mode "$queue_depth")
threshold="${BUSY_THRESHOLD_ISSUES:-1}"
[ "$threshold" -le 0 ] && threshold=1
busy_pct=$(( queue_depth * 100 / threshold ))
[ "$busy_pct" -gt 100 ] && busy_pct=100

prev_mode=$(cat "$STATE_DIR/mode" 2>/dev/null || echo "")
echo "$mode"      > "$STATE_DIR/mode"
echo "$busy_pct"  > "$STATE_DIR/busyness_pct"

# Write per-agent cadences for hive status to read
for _agent in scanner reviewer architect outreach supervisor; do
  _secs=$(get_cadence "$_agent" "$mode")
  if [ "$_secs" -eq 0 ]; then
    echo "paused"
  else
    secs_to_label "$_secs"
  fi > "$STATE_DIR/cadence_${_agent}"
done

# Run model optimizer — writes model state files before agents are kicked
optimize_model_assignment "$mode"

# Report mode transitions
if [ -n "$prev_mode" ] && [ "$prev_mode" != "$mode" ]; then
  log "MODE CHANGE ${prev_mode} → ${mode} (queue=${queue_depth} threshold=${BUSY_THRESHOLD_ISSUES})"
  ntfy "default" \
    "Governor: ${prev_mode} → ${mode}" \
    "Queue depth ${queue_depth} (threshold ${BUSY_THRESHOLD_ISSUES}). Cadences: scanner=$(secs_to_label "$(get_cadence scanner "$mode")") reviewer=$(secs_to_label "$(get_cadence reviewer "$mode")") architect=$(secs_to_label "$(get_cadence architect "$mode")") outreach=$(secs_to_label "$(get_cadence outreach "$mode")")" \
    "arrows_counterclockwise"
fi

log "MODE=${mode} queue=${queue_depth} scanner=$(secs_to_label "$(get_cadence scanner "$mode")") reviewer=$(secs_to_label "$(get_cadence reviewer "$mode")") architect=$(secs_to_label "$(get_cadence architect "$mode")") outreach=$(secs_to_label "$(get_cadence outreach "$mode")")"

# Log model assignments
budget_pct=$(grep '^PROJECTED_PCT=' "$STATE_DIR/budget_state" 2>/dev/null | cut -d= -f2 || echo "?")
log "BUDGET projected=${budget_pct}% models: $(for _a in scanner reviewer architect outreach supervisor; do
  _b=$(grep '^BACKEND=' "$STATE_DIR/model_${_a}" 2>/dev/null | cut -d= -f2 || echo "?")
  _m=$(grep '^MODEL=' "$STATE_DIR/model_${_a}" 2>/dev/null | cut -d= -f2 || echo "?")
  printf '%s=%s:%s ' "$_a" "$_b" "$_m"
done)"

maybe_kick scanner    "$mode"
maybe_kick reviewer   "$mode"
maybe_kick architect  "$mode"
maybe_kick outreach   "$mode"
maybe_kick supervisor "$mode"

log "GOVERNOR DONE"
