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
#     reviewer  → PAUSED
#     architect → PAUSED
#     outreach  → PAUSED
#
#   BUSY (queue > BUSY_THRESHOLD, default 10):
#     scanner   → every 15 min
#     reviewer  → every 1 hour
#     architect → PAUSED
#     outreach  → PAUSED
#
#   QUIET (queue > IDLE_THRESHOLD, default 2):
#     scanner   → every 15 min
#     reviewer  → every 45 min
#     architect → PAUSED
#     outreach  → PAUSED
#
#   IDLE (queue ≤ IDLE_THRESHOLD):
#     scanner   → every 15 min
#     reviewer  → every 15 min
#     architect → every 30 min  (jam — queue is clear)
#     outreach  → every 2 hours
#
# State lives in STATE_DIR (tmpfs — cleared on reboot, fine for kick timing).
# Logs go to journald via stdout + LOG_FILE for human review.

set -euo pipefail

# Source centralized backend/model config
_GOV_SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
_GOV_BACKENDS_CONF="${_GOV_SCRIPT_DIR}/../config/backends.conf"
if [[ -f "$_GOV_BACKENDS_CONF" ]]; then
  # shellcheck source=../config/backends.conf
  source "$_GOV_BACKENDS_CONF"
elif [[ -f /usr/local/etc/hive/backends.conf ]]; then
  source /usr/local/etc/hive/backends.conf
fi

# ── Repos to scan ───────────────────────────────────────────────────────────
# Priority: HIVE_REPOS env var > governor.env > hive-project.yaml > hardcoded default
if [[ -f /etc/hive/governor.env ]]; then
  # shellcheck disable=SC1091
  . /etc/hive/governor.env
fi
# Load from project config if available and HIVE_REPOS not already set
if [[ -z "${HIVE_REPOS:-}" ]]; then
  if [[ -f /usr/local/bin/hive-config.sh ]]; then
    # shellcheck disable=SC1091
    source /usr/local/bin/hive-config.sh
    [[ -n "${PROJECT_REPOS:-}" ]] && HIVE_REPOS="$PROJECT_REPOS"
  fi
fi
if [[ -z "${HIVE_REPOS:-}" ]]; then
  echo "FATAL: HIVE_REPOS is empty. Set it in governor.env, hive-project.yaml, or HIVE_REPOS env var." >&2
  exit 1
fi
IFS=' ' read -ra REPOS <<< "$HIVE_REPOS"

# ── Exempt-label filter ─────────────────────────────────────────────────────
# Issues matching any of these labels are NOT counted toward the actionable queue.
EXEMPT_LABEL_REGEX="${GOVERNOR_EXEMPT_LABELS:-nightly-tests|LFX|do-not-merge|meta-tracker|auto-qa-tuning-report|hold|adopters|changes-requested|waiting-on-author}"

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
CADENCE_SCANNER_IDLE_SEC="${CADENCE_SCANNER_IDLE_SEC:-900}"       # 15 min

CADENCE_REVIEWER_SURGE_SEC="${CADENCE_REVIEWER_SURGE_SEC:-0}"       # PAUSED
CADENCE_REVIEWER_BUSY_SEC="${CADENCE_REVIEWER_BUSY_SEC:-3600}"     # 1 hour
CADENCE_REVIEWER_QUIET_SEC="${CADENCE_REVIEWER_QUIET_SEC:-2700}"   # 45 min
CADENCE_REVIEWER_IDLE_SEC="${CADENCE_REVIEWER_IDLE_SEC:-900}"      # 15 min

CADENCE_ARCHITECT_SURGE_SEC="${CADENCE_ARCHITECT_SURGE_SEC:-0}"     # PAUSED
CADENCE_ARCHITECT_BUSY_SEC="${CADENCE_ARCHITECT_BUSY_SEC:-0}"      # PAUSED
CADENCE_ARCHITECT_QUIET_SEC="${CADENCE_ARCHITECT_QUIET_SEC:-0}"    # PAUSED
CADENCE_ARCHITECT_IDLE_SEC="${CADENCE_ARCHITECT_IDLE_SEC:-1800}"    # 30 min (jam)

CADENCE_SUPERVISOR_SURGE_SEC="${CADENCE_SUPERVISOR_SURGE_SEC:-300}"   # 5 min
CADENCE_SUPERVISOR_BUSY_SEC="${CADENCE_SUPERVISOR_BUSY_SEC:-600}"    # 10 min
CADENCE_SUPERVISOR_QUIET_SEC="${CADENCE_SUPERVISOR_QUIET_SEC:-900}"  # 15 min
CADENCE_SUPERVISOR_IDLE_SEC="${CADENCE_SUPERVISOR_IDLE_SEC:-1800}"   # 30 min

CADENCE_OUTREACH_SURGE_SEC="${CADENCE_OUTREACH_SURGE_SEC:-0}"       # PAUSED
CADENCE_OUTREACH_BUSY_SEC="${CADENCE_OUTREACH_BUSY_SEC:-0}"         # PAUSED
CADENCE_OUTREACH_QUIET_SEC="${CADENCE_OUTREACH_QUIET_SEC:-0}"       # PAUSED
CADENCE_OUTREACH_IDLE_SEC="${CADENCE_OUTREACH_IDLE_SEC:-7200}"      # 2 hours

# ── Token budget ────────────────────────────────────────────────────────────
TOKEN_BUDGET_WEEKLY="${TOKEN_BUDGET_WEEKLY:-200000000}"  # ~200M billable tokens ≈ 100% weekly limit
TOKEN_BUDGET_SAFETY_PCT="${TOKEN_BUDGET_SAFETY_PCT:-85}"
TOKEN_BUDGET_RESET_DAY="${TOKEN_BUDGET_RESET_DAY:-4}"  # 4=Friday (Claude resets Fri 7PM)
TOKEN_COLLECTOR_JSON="/var/run/hive-metrics/tokens.json"
BUDGET_IGNORE_FLAG="/var/run/hive-metrics/budget_ignore"

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
MODEL_SURGE_OUTREACH="${MODEL_SURGE_OUTREACH:-copilot:claude-opus-4-6}"
MODEL_SURGE_SUPERVISOR="${MODEL_SURGE_SUPERVISOR:-claude:claude-haiku-4-5}"

MODEL_BUSY_SCANNER="${MODEL_BUSY_SCANNER:-claude:claude-sonnet-4-6}"
MODEL_BUSY_REVIEWER="${MODEL_BUSY_REVIEWER:-claude:claude-sonnet-4-6}"
MODEL_BUSY_ARCHITECT="${MODEL_BUSY_ARCHITECT:-copilot:claude-sonnet-4-6}"
MODEL_BUSY_OUTREACH="${MODEL_BUSY_OUTREACH:-copilot:claude-sonnet-4-6}"
MODEL_BUSY_SUPERVISOR="${MODEL_BUSY_SUPERVISOR:-claude:claude-haiku-4-5}"

MODEL_QUIET_SCANNER="${MODEL_QUIET_SCANNER:-claude:claude-haiku-4-5}"
MODEL_QUIET_REVIEWER="${MODEL_QUIET_REVIEWER:-copilot:claude-sonnet-4-6}"
MODEL_QUIET_ARCHITECT="${MODEL_QUIET_ARCHITECT:-copilot:claude-opus-4-6}"
MODEL_QUIET_OUTREACH="${MODEL_QUIET_OUTREACH:-copilot:claude-sonnet-4-6}"
MODEL_QUIET_SUPERVISOR="${MODEL_QUIET_SUPERVISOR:-claude:claude-haiku-4-5}"

MODEL_IDLE_SCANNER="${MODEL_IDLE_SCANNER:-copilot:claude-sonnet-4-6}"
MODEL_IDLE_REVIEWER="${MODEL_IDLE_REVIEWER:-copilot:claude-sonnet-4-6}"
MODEL_IDLE_ARCHITECT="${MODEL_IDLE_ARCHITECT:-copilot:claude-opus-4-6}"
MODEL_IDLE_OUTREACH="${MODEL_IDLE_OUTREACH:-copilot:claude-sonnet-4-6}"
MODEL_IDLE_SUPERVISOR="${MODEL_IDLE_SUPERVISOR:-copilot:claude-sonnet-4-6}"

RATE_LIMIT_FALLBACK_BACKEND="${RATE_LIMIT_FALLBACK_BACKEND:-copilot}"
RATE_LIMIT_COOLDOWN="${RATE_LIMIT_COOLDOWN:-1800}"  # 30 min

# ── Paths ───────────────────────────────────────────────────────────────────
STATE_DIR="/var/run/kick-governor"
_is_agent_paused() { hive_is_paused "$1"; }

# Structured audit log — every governor kick decision records pause state
KICK_AUDIT_LOG="/var/log/kick-audit.jsonl"
audit_kick() {
  local agent="$1" action="$2" reason="$3" caller="${4:-governor}"
  local paused_gov=false paused_op=false paused_etc=false
  [[ -f "$STATE_DIR/paused_${agent}" ]] && paused_gov=true
  [[ -f "$STATE_DIR/operator_paused_${agent}" ]] && paused_op=true
  [[ -f "/etc/hive/pause_${agent}" ]] && paused_etc=true
  printf '{"ts":"%s","agent":"%s","action":"%s","reason":"%s","caller":"%s","paused_governor":%s,"paused_operator":%s,"paused_etc":%s}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%S+00:00)" "$agent" "$action" "$reason" "$caller" \
    "$paused_gov" "$paused_op" "$paused_etc" >> "$KICK_AUDIT_LOG"
}
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

detect_running_backend() {
  local session="$1"
  local pane_pid
  pane_pid=$(tmux display-message -t "$session" -p '#{pane_pid}' 2>/dev/null) || return 1
  local cmdline
  cmdline=$(ps -o args= --ppid "$pane_pid" 2>/dev/null | head -1) || return 1
  if echo "$cmdline" | grep -q '/copilot\b\|^copilot\b'; then
    echo "copilot"
  elif echo "$cmdline" | grep -q '/gemini\b\|^gemini\b'; then
    echo "gemini"
  elif echo "$cmdline" | grep -q '/claude\b\|^claude\b'; then
    echo "claude"
  else
    return 1
  fi
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
# Reads from centralized api-collector cache (written by dashboard/api-collector.sh).
# Falls back to per-repo cache files if the main cache is missing.
GITHUB_CACHE="${HIVE_METRICS_DIR:-/var/run/hive-metrics}/github-cache.json"

count_actionable() {
  local repo="$1"
  local name="${repo##*/}"
  local cache_dir="$STATE_DIR/repo_cache"
  local issues prs

  issues=$(cat "$cache_dir/${name}_actionable_issues" 2>/dev/null || echo "0")
  prs=$(cat "$cache_dir/${name}_actionable_prs" 2>/dev/null || echo "0")

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
  # Return issue count only — PRs don't drive mode thresholds (CI+merge is cheap)
  echo "$total_i"
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
  normalize_model_for_backend "$2" "$1"
}

get_model_selection() {
  local agent="$1" mode="$2"
  local upper_mode upper_agent
  upper_mode=$(echo "$mode" | tr '[:lower:]' '[:upper:]')
  upper_agent=$(echo "$agent" | tr '[:lower:]' '[:upper:]')
  local var_name="MODEL_${upper_mode}_${upper_agent}"
  local selection="${!var_name}"
  if [[ -z "$selection" ]]; then
    selection="copilot:claude-sonnet-4-6"
  fi
  echo "$selection"
}

get_cost_weight() {
  local backend="$1" model="$2"
  case "$backend" in
    copilot|goose) echo 0; return ;;
  esac
  case "$(model_tier "$model")" in
    opus)    echo "${COST_WEIGHT_OPUS}" ;;
    sonnet)  echo "${COST_WEIGHT_SONNET}" ;;
    haiku)   echo "${COST_WEIGHT_HAIKU}" ;;
    gpt)     echo 0 ;;
    *)       echo "${COST_WEIGHT_SONNET}" ;;
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

  local hours_left hours_elapsed
  read -r hours_left hours_elapsed <<< "$(python3 -c "
import datetime
now = datetime.datetime.now()
reset_day = $TOKEN_BUDGET_RESET_DAY
days_ahead = (reset_day - now.weekday()) % 7
if days_ahead == 0 and now.hour > 0: days_ahead = 7
reset = (now + datetime.timedelta(days=days_ahead)).replace(hour=0, minute=0, second=0, microsecond=0)
hours_left = max(1, int((reset - now).total_seconds() // 3600))
# Hours since last reset
total_hours = 168
hours_elapsed = max(1, total_hours - hours_left)
print(hours_left, hours_elapsed)
" 2>/dev/null || echo "168 1")"

  # Project using average burn rate over elapsed week, not instantaneous hourly
  local avg_hourly=0
  [[ "$hours_elapsed" -gt 0 ]] && avg_hourly=$((used / hours_elapsed))
  local projected=$((used + avg_hourly * hours_left))
  local projected_pct=0
  [[ "$TOKEN_BUDGET_WEEKLY" -gt 0 ]] && projected_pct=$((projected * 100 / TOKEN_BUDGET_WEEKLY))

  cat > "$budget_file" <<BUDGETEOF
BUDGET_WEEKLY=$TOKEN_BUDGET_WEEKLY
BUDGET_USED=$used
BUDGET_REMAINING=$remaining
BUDGET_PCT_USED=$pct_used
BURN_RATE_HOURLY=$avg_hourly
BURN_RATE_INSTANT=$burn_hourly
HOURS_ELAPSED=$hours_elapsed
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

  if [[ -f "$BUDGET_IGNORE_FLAG" ]]; then
    log "BUDGET: ignored (operator override active)"
  elif (( projected_pct > TOKEN_BUDGET_SAFETY_PCT )); then
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
    # Skip paused agents — don't write model files that trigger restarts
    if _is_agent_paused "$agent"; then
      log "  PAUSED: $agent — skipping model assignment"
      continue
    fi

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

    # Respect pin flags in env files:
    #   AGENT_CLI_PINNED=true  → pin both backend and model (legacy, full lock)
    #   AGENT_PIN_CLI=true     → pin backend only, governor can change model
    #   AGENT_PIN_MODEL=true   → pin model only, governor can change backend
    local env_dir="/etc/hive"
    if grep -q "^AGENT_CLI_PINNED=true" "$env_dir/${agent}.env" 2>/dev/null; then
      log "  PINNED: $agent (both pinned — skipping, creating missing lock)"
      sudo touch "$lock_file"
      continue
    fi

    local gov_pin_cli gov_pin_model
    gov_pin_cli=$(grep -q "^AGENT_PIN_CLI=true" "$env_dir/${agent}.env" 2>/dev/null && echo 1 || echo 0)
    gov_pin_model=$(grep -q "^AGENT_PIN_MODEL=true" "$env_dir/${agent}.env" 2>/dev/null && echo 1 || echo 0)

    if [[ "$gov_pin_cli" == "1" ]]; then
      local pinned_backend
      pinned_backend=$(detect_running_backend "$agent" 2>/dev/null || true)
      if [[ -z "$pinned_backend" ]]; then
        pinned_backend=$(grep '^BACKEND=' "$STATE_DIR/model_${agent}" 2>/dev/null | cut -d= -f2 || true)
      fi
      if [[ -n "$pinned_backend" && "$pinned_backend" != "$backend" ]]; then
        log "  PIN_CLI: $agent backend pinned to $pinned_backend (from process), overriding $backend"
        backend="$pinned_backend"
      fi
    fi
    if [[ "$gov_pin_model" == "1" ]]; then
      local pinned_model
      pinned_model=$(grep '^MODEL=' "$STATE_DIR/model_${agent}" 2>/dev/null | cut -d= -f2 || true)
      if [[ -n "$pinned_model" && "$pinned_model" != "$model" ]]; then
        log "  PIN_MODEL: $agent model pinned to $pinned_model, overriding $model"
        model="$pinned_model"
      fi
    fi

    local cost_weight
    cost_weight=$(get_cost_weight "$backend" "$model")
    local prev_backend prev_model
    prev_backend=$(grep '^BACKEND=' "$STATE_DIR/model_${agent}" 2>/dev/null | cut -d= -f2 || true)
    prev_model=$(grep '^MODEL=' "$STATE_DIR/model_${agent}" 2>/dev/null | cut -d= -f2 || true)

    if [[ "$prev_backend" == "$backend" && "$prev_model" == "$model" ]]; then
      continue
    fi

    cat > "$STATE_DIR/model_${agent}" <<MODELEOF
BACKEND=$backend
MODEL=$model
COST_WEIGHT=$cost_weight
REASON=${override_reasons[$agent]:-${mode}_mode}
PREV_BACKEND=${prev_backend:-}
PREV_MODEL=${prev_model:-}
UPDATED=$(date -Iseconds)
MODELEOF

    if [[ -n "$prev_backend" ]]; then
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

  # Dashboard pause flag — survives governor ticks
  if _is_agent_paused "$agent"; then
    # Track that this agent is paused so we can detect unpause next tick
    touch "$STATE_DIR/was_paused_${agent}"
    log "SKIP ${agent} (mode=${mode} — DASHBOARD PAUSED)"
    audit_kick "$agent" "SKIP" "dashboard-paused" "governor"
    return
  fi

  # Detect unpause — if agent was paused last tick but isn't now, kick immediately
  if [[ -f "$STATE_DIR/was_paused_${agent}" ]]; then
    rm -f "$STATE_DIR/was_paused_${agent}"
    log "UNPAUSE ${agent} — kicking immediately"
    audit_kick "$agent" "KICK" "unpause-immediate" "governor"
    ntfy "default" "Unpause: ${agent}" "Agent unpaused — sending immediate kick" "arrow_forward"
    if "$KICK_SCRIPT" "$agent" 2>&1 \
        | while IFS= read -r line; do log "  [${agent}] ${line}"; done; then
      record_kick "$agent"
    fi
    return
  fi

  if [ "$cadence" -eq 0 ]; then
    log "SKIP ${agent} (mode=${mode} — PAUSED)"
    audit_kick "$agent" "SKIP" "cadence-zero" "governor"
    return
  fi

  if [ "$elapsed" -ge "$cadence" ]; then
    local next_et
    next_et=$(TZ=America/New_York date -d "+${cadence} seconds" '+%H:%M %Z')
    audit_kick "$agent" "KICK" "mode=${mode}" "governor"
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
    log "SKIP ${agent} (mode=${mode} next in $(secs_to_label "$remaining"))"
    audit_kick "$agent" "SKIP" "cadence-not-elapsed" "governor"
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
  if _is_agent_paused "$_agent"; then
    echo "paused" > "$STATE_DIR/cadence_${_agent}"
    continue
  fi
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

# Clear stuck input on every tick — C-c + C-u to discard, not Enter to execute.
# If C-c + C-u fails (buffer completely stuck), flag agent for restart.
BUFFER_STUCK_HEARTBEAT_THRESHOLD=1200  # 20 minutes — skip buffer-stuck flag if status file is fresher
for _fa in scanner reviewer supervisor architect outreach; do
  _is_agent_paused "$_fa" && continue
  tmux has-session -t "$_fa" 2>/dev/null || continue
  _pane_text=$(tmux capture-pane -t "$_fa" -p 2>/dev/null || true)
  _prompt_line=$(echo "$_pane_text" | grep "❯" | tail -1 || true)
  _after=$(echo "$_prompt_line" | sed 's/.*❯[[:space:]]*//')
  if [ -n "$_after" ] && [ ${#_after} -gt 2 ]; then
    log "CLEAR ${_fa} — discarding ${#_after} chars of stale input (C-c + C-u)"
    tmux send-keys -t "$_fa" C-c 2>/dev/null || true
    sleep 1
    tmux send-keys -t "$_fa" C-u 2>/dev/null || true
    sleep 2
    # Verify clear worked — if text persists, buffer is frozen
    _after2=$(tmux capture-pane -t "$_fa" -p 2>/dev/null | grep "❯" | tail -1 | sed 's/.*❯[[:space:]]*//')
    if [ -n "$_after2" ] && [ ${#_after2} -gt 2 ]; then
      # Check status file heartbeat before flagging — agent may be working despite pane text
      _bf_status_file="$HOME/.hive/${_fa}_status.txt"
      _bf_updated=$(grep '^UPDATED=' "$_bf_status_file" 2>/dev/null | cut -d= -f2 || true)
      _bf_skip=false
      if [ -n "$_bf_updated" ]; then
        _bf_epoch=$(date -d "$_bf_updated" +%s 2>/dev/null || echo 0)
        _bf_now=$(date +%s)
        _bf_age=$(( _bf_now - _bf_epoch ))
        if (( _bf_age < BUFFER_STUCK_HEARTBEAT_THRESHOLD )); then
          log "SKIP-STUCK ${_fa} — buffer frozen but status file fresh (${_bf_age}s old), not flagging"
          _bf_skip=true
        fi
      fi
      if [ "$_bf_skip" = "false" ]; then
        log "STUCK ${_fa} — buffer frozen (${#_after2} chars), flagging for restart"
        touch "$STATE_DIR/needs_restart_${_fa}"
      fi
    fi
  fi
done

# ── Stale-status escalation ─────────────────────────────────────────────────
# Check each agent's status file. If UPDATED is >20 min stale and STATUS is
# not DONE/DONE_WITH_CONCERNS, flag as stuck.
STALE_THRESHOLD_SEC=1200  # 20 minutes
STUCK_COUNT_FILE="$STATE_DIR/stuck_counts"
touch "$STUCK_COUNT_FILE" 2>/dev/null || true

for _sa in scanner reviewer architect outreach supervisor; do
  _is_agent_paused "$_sa" && continue
  _status_file="$HOME/.hive/${_sa}_status.txt"
  [[ ! -f "$_status_file" ]] && continue

  _sa_status=$(grep '^STATUS=' "$_status_file" 2>/dev/null | cut -d= -f2 || true)
  _sa_updated=$(grep '^UPDATED=' "$_status_file" 2>/dev/null | cut -d= -f2 || true)

  [[ -z "$_sa_updated" ]] && continue
  [[ "$_sa_status" == "DONE" || "$_sa_status" == "DONE_WITH_CONCERNS" ]] && continue

  _sa_epoch=$(date -d "$_sa_updated" +%s 2>/dev/null || echo 0)
  _now_epoch=$(date +%s)
  _sa_age=$(( _now_epoch - _sa_epoch ))

  if (( _sa_age > STALE_THRESHOLD_SEC )); then
    _prev_stuck=$(grep "^${_sa}:" "$STUCK_COUNT_FILE" 2>/dev/null | cut -d: -f2 || echo 0)
    _new_stuck=$(( _prev_stuck + 1 ))
    sed -i "/^${_sa}:/d" "$STUCK_COUNT_FILE" 2>/dev/null || true
    echo "${_sa}:${_new_stuck}" >> "$STUCK_COUNT_FILE"

    log "STALE ${_sa} — status file ${_sa_age}s old (threshold ${STALE_THRESHOLD_SEC}s), stuck count=${_new_stuck}"
    ntfy "default" "Stale: ${_sa}" "${_sa} status unchanged for ${_sa_age}s (status=${_sa_status}). Stuck count: ${_new_stuck}" "warning"

    if (( _new_stuck >= 3 )); then
      log "ESCALATE ${_sa} — stuck ${_new_stuck}x consecutively, flagging for restart"
      touch "$STATE_DIR/needs_restart_${_sa}"
      ntfy "high" "Stuck: ${_sa}" "${_sa} stuck ${_new_stuck}x consecutively. Flagging for restart." "rotating_light"
      sed -i "/^${_sa}:/d" "$STUCK_COUNT_FILE" 2>/dev/null || true
      echo "${_sa}:0" >> "$STUCK_COUNT_FILE"
    fi
  else
    sed -i "/^${_sa}:/d" "$STUCK_COUNT_FILE" 2>/dev/null || true
    echo "${_sa}:0" >> "$STUCK_COUNT_FILE"
  fi
done

maybe_kick scanner    "$mode"
maybe_kick reviewer   "$mode"
maybe_kick architect  "$mode"
maybe_kick outreach   "$mode"
maybe_kick supervisor "$mode"

log "GOVERNOR DONE"
