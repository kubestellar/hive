#!/bin/bash
# kick-agents.sh — fires work orders at the scanner, reviewer, architect, and outreach tmux sessions.
# Called by systemd timers (or manually). Does NOT require Claude to be running
# as a supervisor — it speaks directly to the named tmux sessions.
#
# Usage:
#   kick-agents.sh scanner    # kick scanner only
#   kick-agents.sh reviewer   # kick reviewer only
#   kick-agents.sh architect  # kick architect only
#   kick-agents.sh outreach   # kick outreach only
#   kick-agents.sh all        # kick all four (default)
#
# Systemd timer fires this every 15 min for scanner, every 30 min for reviewer,
# every 2 hours for architect and outreach.

set -euo pipefail

TARGET="${1:-all}"
TMUX_BIN="${TMUX_BIN:-tmux}"
LOG="/var/log/kick-agents.log"
TIMESTAMP="$(TZ=America/New_York date '+%Y-%m-%d %I:%M:%S %p %Z')"
ET_NOW="$(TZ=America/New_York date '+%I:%M %p ET')"
NTFY_TOPIC="${NTFY_TOPIC:-ntfy.sh/hive}"
NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"
SLACK_WEBHOOK="${SLACK_WEBHOOK:-}"
DISCORD_WEBHOOK="${DISCORD_WEBHOOK:-}"
NOTIFY_LIB="${NOTIFY_LIB:-/usr/local/bin/notify.sh}"
[ -f "$NOTIFY_LIB" ] && . "$NOTIFY_LIB"

# Backend state directory — tracks which backend each agent is currently using.
# On rate limit, the agent switches to its fallback backend.
BACKEND_STATE_DIR="/var/run/agent-backends"
mkdir -p "$BACKEND_STATE_DIR" 2>/dev/null || true

# Agent handoff state — captures last N lines of work context when switching backends
HANDOFF_DIR="/tmp/agent-handoff"
mkdir -p "$HANDOFF_DIR" 2>/dev/null || true

log() { echo "[$TIMESTAMP] $*" | tee -a "$LOG"; }
ntfy() { notify "$1" "$2"; }  # legacy shim — use notify() directly for new code

# ── Backend management ──────────────────────────────────────────────
# Each agent has a primary and fallback backend. State is tracked in
# /var/run/agent-backends/<agent> (contains "claude" or "copilot").

# Default backend assignments per agent
declare -A AGENT_PRIMARY_BACKEND=(
  [scanner]=copilot
  [reviewer]=claude
  [architect]=claude
  [outreach]=claude
)
declare -A AGENT_FALLBACK_BACKEND=(
  [scanner]=claude
  [reviewer]=copilot
  [architect]=copilot
  [outreach]=copilot
)
# Model to use per backend — Copilot uses dots, Claude uses hyphens
declare -A BACKEND_MODEL=(
  [copilot]=claude-opus-4-6
  [claude]=claude-sonnet-4-5
)
# Scanner runs Opus on both backends
declare -A AGENT_MODEL_OVERRIDE=(
  [scanner-copilot]=claude-opus-4-6
  [scanner-claude]=claude-opus-4-6
)
declare -A MODEL_SWITCHED=()

get_current_backend() {
  local agent="$1"
  if [ -f "$BACKEND_STATE_DIR/$agent" ]; then
    cat "$BACKEND_STATE_DIR/$agent"
  else
    echo "${AGENT_PRIMARY_BACKEND[$agent]:-claude}"
  fi
}

set_current_backend() {
  local agent="$1" backend="$2"
  echo "$backend" > "$BACKEND_STATE_DIR/$agent"
}

get_model_for() {
  local agent="$1" backend="$2"
  local override_key="${agent}-${backend}"
  if [ -n "${AGENT_MODEL_OVERRIDE[$override_key]+x}" ]; then
    echo "${AGENT_MODEL_OVERRIDE[$override_key]}"
  else
    echo "${BACKEND_MODEL[$backend]}"
  fi
}

capture_handoff_state() {
  local session="$1" agent="$2"
  local handoff_file="$HANDOFF_DIR/${agent}-handoff.md"
  local pane_text
  pane_text=$($TMUX_BIN capture-pane -t "$session" -p -S -200 2>/dev/null || true)
  if [ -n "$pane_text" ]; then
    cat > "$handoff_file" <<HANDOFF_EOF
# Agent Handoff — $agent
# Captured at: $(date -Is)
# Reason: Backend switch due to rate limit

## Last 200 lines of session output:
\`\`\`
$pane_text
\`\`\`

## Instructions
Continue where the previous session left off. Read your CLAUDE.md for standing instructions.
HANDOFF_EOF
    log "HANDOFF $agent — saved context to $handoff_file"
  fi
}

switch_backend() {
  local session="$1" agent="$2"
  local current_backend fallback_backend model

  current_backend=$(get_current_backend "$agent")
  fallback_backend="${AGENT_FALLBACK_BACKEND[$agent]:-claude}"

  if [ "$current_backend" = "$fallback_backend" ]; then
    fallback_backend="${AGENT_PRIMARY_BACKEND[$agent]:-claude}"
  fi

  model=$(get_model_for "$agent" "$fallback_backend")

  log "SWITCH $agent: $current_backend → $fallback_backend (model: $model)"
  ntfy "$agent — switching backend" "Rate limited on $current_backend. Switching to $fallback_backend ($model)"

  capture_handoff_state "$session" "$agent"

  $TMUX_BIN send-keys -t "$session" Escape 2>/dev/null || true
  sleep 2
  $TMUX_BIN send-keys -t "$session" -l "/exit" 2>/dev/null || true
  sleep 1
  $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true
  sleep 3

  $TMUX_BIN send-keys -t "$session" -l "agent-launch.sh --backend $fallback_backend --model $model" 2>/dev/null || true
  sleep 1
  $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true

  set_current_backend "$agent" "$fallback_backend"

  local SWITCH_STARTUP_WAIT=90
  local SWITCH_POLL=3
  local sw_waited=0
  log "SWITCH $agent — waiting up to ${SWITCH_STARTUP_WAIT}s for $fallback_backend CLI to start"
  while (( sw_waited < SWITCH_STARTUP_WAIT )); do
    if session_cli_ready "$session"; then
      log "SWITCH $agent — $fallback_backend CLI ready after ${sw_waited}s"
      break
    fi
    sleep "$SWITCH_POLL"
    (( sw_waited += SWITCH_POLL ))
  done
  if (( sw_waited >= SWITCH_STARTUP_WAIT )); then
    log "SWITCH $agent — $fallback_backend CLI did not start within ${SWITCH_STARTUP_WAIT}s"
  fi
}

session_exists() {
  $TMUX_BIN has-session -t "$1" 2>/dev/null
}

session_idle() {
  # Returns 0 (idle) if the pane contains the Claude Code idle prompt (❯)
  # The prompt is ❯ (U+276F) followed by a non-breaking space (U+00A0)
  # Check full pane to account for status bar lines below the prompt
  $TMUX_BIN capture-pane -t "$1" -p | grep -q "❯"
}

flush_pending_input() {
  # Detect text stuck in the input line (sent without -l or missing Enter).
  # If the last ❯ line has trailing text, the agent has unsent input — send Enter.
  local session="$1"
  local pane_text
  pane_text=$($TMUX_BIN capture-pane -t "$session" -p 2>/dev/null || true)
  local prompt_line
  prompt_line=$(echo "$pane_text" | grep "❯" | tail -1)
  if [ -n "$prompt_line" ]; then
    local after_prompt
    after_prompt=$(echo "$prompt_line" | sed 's/.*❯[[:space:]]*//')
    if [ -n "$after_prompt" ] && [ ${#after_prompt} -gt 2 ]; then
      log "FLUSH $session — found unsent input (${#after_prompt} chars), sending Enter"
      $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true
      sleep 2
      return 0
    fi
  fi
  return 1
}

session_cli_ready() {
  # Returns 0 if the CLI has fully started (not just shell prompt visible).
  # After a model switch, the old scrollback still has ❯ from the previous
  # session, so session_idle returns true before the new CLI loads. This
  # function checks for actual CLI startup markers AND the idle prompt.
  local pane_text
  pane_text=$($TMUX_BIN capture-pane -t "$1" -p 2>/dev/null || true)
  # Must have BOTH: a CLI startup banner AND the idle prompt
  if echo "$pane_text" | grep -qE "Environment loaded|Describe a task|custom instructions"; then
    if echo "$pane_text" | grep -q "❯"; then
      return 0
    fi
  fi
  return 1
}

next_run() {
  # Compute next run time in ET for a given agent
  case "$1" in
    scanner)  systemctl show kick-scanner.timer  --property=NextElapseUSecRealtime --value 2>/dev/null | xargs -I{} date -d "{}" '+%I:%M %p ET' 2>/dev/null || echo "unknown" ;;
    reviewer) systemctl show kick-reviewer.timer  --property=NextElapseUSecRealtime --value 2>/dev/null | xargs -I{} date -d "{}" '+%I:%M %p ET' 2>/dev/null || echo "unknown" ;;
    architect) systemctl show kick-architect.timer --property=NextElapseUSecRealtime --value 2>/dev/null | xargs -I{} date -d "{}" '+%I:%M %p ET' 2>/dev/null || echo "unknown" ;;
    outreach) systemctl show kick-outreach.timer --property=NextElapseUSecRealtime --value 2>/dev/null | xargs -I{} date -d "{}" '+%I:%M %p ET' 2>/dev/null || echo "unknown" ;;
  esac
}

check_rate_limit() {
  # After a kick, wait and check if the session hit a CLAUDE/COPILOT CLI rate limit.
  # If so, parse the reset time and schedule a re-kick.
  # Error format: "You're out of extra usage · resets 3am (UTC)"
  #           or: "resets 12:30pm (UTC)"
  #
  # IMPORTANT DISTINCTION — two kinds of rate limits exist:
  #   1. Claude/Copilot CLI usage limits (handled HERE) — the AI backend is exhausted.
  #      Patterns: "You're out of extra usage", "out of extra usage", "resets Xam/pm".
  #      Action: switch backend, schedule re-kick after reset.
  #   2. GitHub API rate limits (handled by gh-rate-check.sh) — the gh CLI hit GitHub's
  #      REST/GraphQL throttle. Patterns: "API rate limit exceeded", "secondary rate limit",
  #      "403.*rate", "Resource not accessible".
  #      Action: do NOT restart — agent should wait/retry/use cache. See GH_RATE_LIMIT_INSTRUCTIONS.
  #
  # The grep patterns below match ONLY category 1 (CLI limits).
  # Category 2 is detected separately by /tmp/hive/bin/gh-rate-check.sh.
  local session="$1"
  local agent="$2"
  local delay_secs="${3:-30}"

  (
    sleep "$delay_secs"
    local pane_text
    pane_text=$($TMUX_BIN capture-pane -t "$session" -p 2>/dev/null || true)

    # Match Claude/Copilot CLI exhaustion messages ONLY.
    # These patterns are specific to AI backend usage limits and will NOT match
    # GitHub API rate limit messages ("API rate limit exceeded", "secondary rate limit", etc.).
    # GitHub API limits are handled by gh-rate-check.sh and should not trigger a backend switch.
    local limit_line
    limit_line=$(echo "$pane_text" | grep -iE "you('re| are) out of|out of extra usage|extra usage.*resets|resets [0-9]+(:[0-9]+)?[aApP][mM]" | tail -1 || true)

    if [ -z "$limit_line" ]; then
      return 0
    fi

    log "RATE-LIMITED $session — $limit_line"

    # Extract reset time — matches patterns like "resets 3am", "resets 12:30pm", "resets 3am (UTC)"
    local reset_time
    reset_time=$(echo "$limit_line" | grep -oP 'resets\s+\K[0-9]{1,2}(:[0-9]{2})?\s*[aApP][mM]' || true)

    if [ -z "$reset_time" ]; then
      ntfy "$agent — rate limited" "Hit rate limit but could not parse reset time. Manual re-kick needed."
      log "RATE-LIMITED $session — could not parse reset time from: $limit_line"
      return 0
    fi

    # Convert reset time (UTC) to epoch seconds
    # Normalize: "3am" -> "3:00 AM", "12:30pm" -> "12:30 PM"
    local normalized
    normalized=$(echo "$reset_time" | sed -E 's/([aApP])([mM])/\U\1\U\2/; s/([0-9])([AP])/\1 \2/')
    # If no colon, add :00
    if ! echo "$normalized" | grep -q ":"; then
      normalized=$(echo "$normalized" | sed -E 's/([0-9]+)/\1:00/')
    fi

    local reset_epoch
    reset_epoch=$(TZ=UTC date -d "today $normalized" +%s 2>/dev/null || true)

    # If the parsed time is in the past, it means tomorrow
    local now_epoch
    now_epoch=$(date +%s)
    if [ -n "$reset_epoch" ] && [ "$reset_epoch" -le "$now_epoch" ]; then
      reset_epoch=$(TZ=UTC date -d "tomorrow $normalized" +%s 2>/dev/null || true)
    fi

    if [ -z "$reset_epoch" ]; then
      ntfy "$agent — rate limited" "Hit rate limit, resets at $reset_time UTC. Could not schedule re-kick."
      log "RATE-LIMITED $session — could not compute epoch for: $reset_time"
      return 0
    fi

    # Schedule re-kick 60 seconds after reset
    local rekick_epoch=$((reset_epoch + 60))
    local wait_secs=$((rekick_epoch - now_epoch))
    local reset_et
    reset_et=$(TZ=America/New_York date -d "@$reset_epoch" '+%I:%M %p ET' 2>/dev/null || echo "$reset_time UTC")

    log "RATE-LIMITED $session — resets $reset_time UTC ($reset_et), wait ${wait_secs}s"

    # Strategy: switch to fallback backend immediately, AND schedule a
    # re-kick on the original backend after the rate limit resets.
    switch_backend "$session" "$agent"

    # After the new backend starts, kick it with the agent's work order
    sleep 15
    /usr/local/bin/kick-agents.sh "$agent"

    # Also schedule a switch back to the primary backend after rate limit resets
    sleep "$wait_secs"
    local current_after_switch
    current_after_switch=$(get_current_backend "$agent")
    local primary="${AGENT_PRIMARY_BACKEND[$agent]:-claude}"
    if [ "$current_after_switch" != "$primary" ]; then
      log "RATE-LIMIT RESET $agent — switching back to primary ($primary)"
      switch_backend "$session" "$agent"
      sleep 15
      /usr/local/bin/kick-agents.sh "$agent"
    fi
  ) &
}

kick() {
  local session="$1"
  local message="$2"
  local agent="$3"

  # After model switch, poll for the session to reappear before checking existence.
  # apply_model_if_changed() sends /exit + agent-launch.sh, which kills the old
  # session and starts a new one. Without polling, session_exists fails because
  # the new CLI hasn't created its tmux session yet.
  if [[ "${MODEL_SWITCHED[$agent]:-}" == "1" ]]; then
    local MODEL_SWITCH_STARTUP_WAIT=90
    local POLL_INTERVAL=3
    local waited=0
    log "MODEL SWITCH $agent — waiting up to ${MODEL_SWITCH_STARTUP_WAIT}s for CLI to fully start"
    while (( waited < MODEL_SWITCH_STARTUP_WAIT )); do
      if session_exists "$session" && session_cli_ready "$session"; then
        log "MODEL SWITCH $agent — CLI ready after ${waited}s"
        break
      fi
      sleep "$POLL_INTERVAL"
      (( waited += POLL_INTERVAL ))
    done
    if (( waited >= MODEL_SWITCH_STARTUP_WAIT )); then
      log "MODEL SWITCH $agent — CLI did not start within ${MODEL_SWITCH_STARTUP_WAIT}s, kicking anyway"
    fi
    MODEL_SWITCHED[$agent]=0
  fi

  if ! session_exists "$session"; then
    log "SKIP $session — session not found"
    ntfy "$agent — not found" "Session $session does not exist. Next try: $(next_run "$agent")"
    return
  fi

  if ! session_idle "$session"; then
    # Check if session is stuck on a Claude/Copilot CLI rate limit (NOT GitHub API rate limit).
    # These patterns match AI backend exhaustion only. GitHub API rate limits
    # ("API rate limit exceeded", "secondary rate limit") are detected by gh-rate-check.sh
    # and should NOT trigger a backend switch — the agent should wait/retry instead.
    local pane_text
    pane_text=$($TMUX_BIN capture-pane -t "$session" -p 2>/dev/null || true)
    if echo "$pane_text" | grep -qiE "you('re| are) out of|out of extra usage|extra usage.*resets"; then
      log "RATE-LIMITED $session — switching backend (CLI usage exhausted)"
      switch_backend "$session" "$agent"
      sleep 15
      /usr/local/bin/kick-agents.sh "$agent"
      return
    fi
    log "SKIP $session — already working"
    ntfy "$agent — busy" "Still working, skipped kick at $ET_NOW. Next: $(next_run "$agent")"
    return
  fi

  flush_pending_input "$session" || true

  log "KICK $session"
  $TMUX_BIN send-keys -t "$session" -l "$message"
  sleep 2  # let tmux flush long message text before sending Enter
  $TMUX_BIN send-keys -t "$session" Enter
  # Verify Enter was delivered — retry if text still in prompt after 3s
  sleep 3
  local _vline _vtext
  _vline=$($TMUX_BIN capture-pane -t "$session" -p 2>/dev/null | grep "❯" | tail -1)
  _vtext=$(echo "$_vline" | sed 's/.*❯[[:space:]]*//')
  if [ -n "$_vtext" ] && [ ${#_vtext} -gt 2 ]; then
    log "RETRY $session — Enter not delivered, resending"
    $TMUX_BIN send-keys -t "$session" Enter
  fi
  ntfy "$agent started" "Kicked at $ET_NOW. Next: $(next_run "$agent")"

  # Background check for rate limit after kick settles
  check_rate_limit "$session" "$agent" 60
}

# GitHub API rate limit handling instructions — included in every agent's kick message.
# These are DIFFERENT from Claude/Copilot CLI usage limits. GitHub API limits should
# never cause an agent restart — they should be worked around.
GH_RATE_LIMIT_INSTRUCTIONS="GITHUB RATE LIMITS — if gh commands fail with rate limit errors \
(API rate limit exceeded, secondary rate limit, 403 rate, Resource not accessible), \
do NOT stop working. Strategies: (1) wait 60s and retry, (2) use 'gh api' with '--cache 1h' \
for read operations, (3) switch from GraphQL to REST or vice versa, (4) continue with \
non-GitHub work while waiting. NEVER treat a GitHub rate limit as a reason to stop your pass."

# ── Compact kick messages ──────────────────────────────────────────
# Standing instructions (beads, PR sweep, hold rules, rate limits, ntfy,
# exec summary) live in each agent's CLAUDE.md — NOT repeated here.
# Kick messages are short actionable directives only.

PULL_AND_READ="cd /tmp/hive && git pull --rebase origin main. Re-read your CLAUDE.md for updated instructions."

SCANNER_BEADS="/home/dev/scanner-beads"
SCANNER_MSG="[AGENT:scanner] ${PULL_AND_READ} Beads: cd ${SCANNER_BEADS} && bd list --json. \
Scan all 5 repos oldest-first. Fix every open issue (Agent tool + worktrees). Merge green AI PRs. Full playbook: /tmp/hive/examples/kubestellar/agents/scanner-CLAUDE.md"

REVIEWER_BEADS="/home/dev/reviewer-beads"
# Build live health preamble — tells reviewer exactly what's red RIGHT NOW
_rh_json=$(/tmp/hive/dashboard/health-check.sh 2>/dev/null || echo '{}')
_rh_reds=""
_rh_ci=$(echo "$_rh_json" | jq -r '.ci // 0' 2>/dev/null || echo 0)
[ "$_rh_ci" -lt 100 ] && _rh_reds="${_rh_reds} CI=${_rh_ci}%"
for _rk in nightly nightlyCompliance nightlyDashboard nightlyPlaywright hourly weekly nightlyRel weeklyRel; do
  _rv=$(echo "$_rh_json" | jq -r ".${_rk} // -1" 2>/dev/null || echo -1)
  [ "$_rv" = "0" ] && _rh_reds="${_rh_reds} ${_rk}=RED"
done
for _dk in vllm pokprod; do
  _dv=$(echo "$_rh_json" | jq -r ".${_dk} // -1" 2>/dev/null || echo -1)
  [ "$_dv" = "0" ] && _rh_reds="${_rh_reds} deploy:${_dk}=RED"
done
_rh_cvg=$(curl -sf "${BADGE_URL:-https://gist.githubusercontent.com/clubanderson/b9a9ae8469f1897a22d5a40629bc1e82/raw/coverage-badge.json}" 2>/dev/null | jq -r '.message // "0"' | tr -d '%' || echo 0)
[ "${_rh_cvg:-0}" -lt 91 ] && _rh_reds="${_rh_reds} coverage=${_rh_cvg}%<91%"
if [ -n "$_rh_reds" ]; then
  _HEALTH_PREAMBLE="URGENT RED:${_rh_reds}. FIX these — open PRs, not reports. "
else
  _HEALTH_PREAMBLE=""
fi
REVIEWER_MSG="[AGENT:reviewer] ${_HEALTH_PREAMBLE}${PULL_AND_READ} Beads: cd ${REVIEWER_BEADS} && bd list --json. \
Run health-check.sh, diagnose every red, open fix PRs. Merge green AI PRs. Full playbook: /tmp/hive/examples/kubestellar/agents/reviewer-CLAUDE.md"

ARCHITECT_BEADS="/home/dev/architect-beads"
ARCHITECT_MSG="[AGENT:architect] ${PULL_AND_READ} Beads: cd ${ARCHITECT_BEADS} && bd list --json. \
Scan for refactor/perf opportunities. No breaking changes, no OAuth, no update system. Full playbook: /tmp/hive/examples/kubestellar/agents/architect-CLAUDE.md"

OUTREACH_BEADS="/home/dev/outreach-beads"
OUTREACH_MSG="[AGENT:outreach] ${PULL_AND_READ} Beads: cd ${OUTREACH_BEADS} && bd list --json. \
Run outreach pass — awesome lists, directories, CNCF landscape. Full playbook: /tmp/hive/examples/kubestellar/agents/outreach-CLAUDE.md"

# ── Governor model integration ──────────────────────────────────────
# Reads /var/run/kick-governor/model_<agent> written by the governor's
# optimize_model_assignment(). Uses in-CLI /model command when possible
# to avoid disrupting agent work. Only restarts when the backend binary
# itself changes (e.g., claude → copilot).
GOVERNOR_STATE_DIR="/var/run/kick-governor"

apply_model_if_changed() {
  local agent="$1" session="$2"

  # Respect manual CLI pin -- operator used hive switch or dashboard dropdown
  local pin_file
  case "$agent" in
    scanner) pin_file="/etc/hive/scanner.env" ;;
    *) pin_file="/etc/hive/${agent}.env" ;;
  esac
  if grep -q "^AGENT_CLI_PINNED=true" "$pin_file" 2>/dev/null; then
    return 0
  fi
  local model_file="$GOVERNOR_STATE_DIR/model_${agent}"
  [[ ! -f "$model_file" ]] && return 0

  local gov_backend gov_model
  gov_backend=$(grep '^BACKEND=' "$model_file" 2>/dev/null | cut -d= -f2)
  gov_model=$(grep '^MODEL=' "$model_file" 2>/dev/null | cut -d= -f2)
  [[ -z "$gov_backend" || -z "$gov_model" ]] && return 0

  local cur_backend
  cur_backend=$(get_current_backend "$agent")
  local cur_model
  cur_model=$(get_model_for "$agent" "$cur_backend")

  if [[ "$cur_backend" == "$gov_backend" && "$cur_model" == "$gov_model" ]]; then
    return 0
  fi

  if ! session_exists "$session"; then
    set_current_backend "$agent" "$gov_backend"
    BACKEND_MODEL[$gov_backend]="$gov_model"
    AGENT_MODEL_OVERRIDE["${agent}-${gov_backend}"]="$gov_model"
    return 0
  fi

  # Never interrupt a working agent — defer all model changes until idle
  if ! session_idle "$session"; then
    log "MODEL DEFER $agent: ${cur_backend}:${cur_model} → ${gov_backend}:${gov_model} — agent busy, will apply when idle"
    return 0
  fi

  log "MODEL SWITCH $agent: ${cur_backend}:${cur_model} → ${gov_backend}:${gov_model} (agent idle, restarting)"

  capture_handoff_state "$session" "$agent"

  $TMUX_BIN send-keys -t "$session" -l "/exit" 2>/dev/null || true
  sleep 1
  $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true
  sleep 3

  $TMUX_BIN send-keys -t "$session" -l "agent-launch.sh --backend $gov_backend --model $gov_model" 2>/dev/null || true
  sleep 1
  $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true

  set_current_backend "$agent" "$gov_backend"
  BACKEND_MODEL[$gov_backend]="$gov_model"
  AGENT_MODEL_OVERRIDE["${agent}-${gov_backend}"]="$gov_model"

  log "MODEL SWITCH $agent — relaunched with ${gov_backend}:${gov_model}, will inject kick prompt after startup"
  MODEL_SWITCHED[$agent]=1
  return 0
}

_now_et=$(TZ=America/New_York date '+%Y-%m-%d %I:%M %p %Z')
SUPERVISOR_MSG="[AGENT:supervisor] MONITORING PASS — ${_now_et}. \
Check all agent panes for stalls/questions (tmux capture-pane -t <session> -p | tail -20). \
Merge green AI PRs. Unstick idle agents. 12h clock only. bd dolt push when done. \
Full playbook: /tmp/hive/examples/kubestellar/agents/supervisor-CLAUDE.md"

case "$TARGET" in
  scanner)
    apply_model_if_changed "scanner" "scanner" && kick "scanner" "$SCANNER_MSG" "scanner"
    ;;
  reviewer)
    apply_model_if_changed "reviewer" "reviewer" && kick "reviewer" "$REVIEWER_MSG" "reviewer"
    ;;
  architect)
    apply_model_if_changed "architect" "architect" && kick "architect" "$ARCHITECT_MSG" "architect"
    ;;
  outreach)
    apply_model_if_changed "outreach" "outreach" && kick "outreach" "$OUTREACH_MSG" "outreach"
    ;;
  supervisor)
    apply_model_if_changed "supervisor" "supervisor" && kick "supervisor" "$SUPERVISOR_MSG" "supervisor"
    ;;
  all)
    apply_model_if_changed "scanner" "scanner" && kick "scanner" "$SCANNER_MSG" "scanner"
    apply_model_if_changed "reviewer" "reviewer" && kick "reviewer" "$REVIEWER_MSG" "reviewer"
    apply_model_if_changed "architect" "architect" && kick "architect" "$ARCHITECT_MSG" "architect"
    apply_model_if_changed "outreach" "outreach" && kick "outreach" "$OUTREACH_MSG" "outreach"
    # supervisor is NOT kicked in "all" — it has its own cadence via governor
    ;;
  *)
    echo "Usage: $0 [scanner|reviewer|architect|outreach|supervisor|all]" >&2
    exit 1
    ;;
esac

bd dolt push 2>&1 | tee -a "$LOG" || log "WARN: bd dolt push failed (non-fatal)"
log "DONE"
