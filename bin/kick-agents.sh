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
TIMESTAMP="$(TZ=America/New_York date '+%Y-%m-%d %H:%M:%S %Z')"
ET_NOW="$(TZ=America/New_York date '+%I:%M %p ET')"
NTFY_TOPIC="ntfy.sh/issue-scanner"

# Backend state directory — tracks which backend each agent is currently using.
# On rate limit, the agent switches to its fallback backend.
BACKEND_STATE_DIR="/var/run/agent-backends"
mkdir -p "$BACKEND_STATE_DIR" 2>/dev/null || true

# Agent handoff state — captures last N lines of work context when switching backends
HANDOFF_DIR="/tmp/agent-handoff"
mkdir -p "$HANDOFF_DIR" 2>/dev/null || true

log() { echo "[$TIMESTAMP] $*" | tee -a "$LOG"; }
ntfy() { curl -s -H "Title: $1" -d "$2" "$NTFY_TOPIC" > /dev/null 2>&1 || true; }

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
  [copilot]=claude-opus-4.6
  [claude]=claude-sonnet-4-5
)
# Scanner runs Opus on both backends
declare -A AGENT_MODEL_OVERRIDE=(
  [scanner-copilot]=claude-opus-4.6
  [scanner-claude]=claude-opus-4-6
)

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
  $TMUX_BIN send-keys -t "$session" "/exit" 2>/dev/null || true
  $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true
  sleep 3

  $TMUX_BIN send-keys -t "$session" "agent-launch.sh --backend $fallback_backend --model $model" 2>/dev/null || true
  $TMUX_BIN send-keys -t "$session" Enter 2>/dev/null || true

  set_current_backend "$agent" "$fallback_backend"

  log "SWITCH $agent — launched $fallback_backend in $session"
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
  # After a kick, wait and check if the session hit a rate limit.
  # If so, parse the reset time and schedule a re-kick.
  # Error format: "You're out of extra usage · resets 3am (UTC)"
  #           or: "resets 12:30pm (UTC)"
  local session="$1"
  local agent="$2"
  local delay_secs="${3:-30}"

  (
    sleep "$delay_secs"
    local pane_text
    pane_text=$($TMUX_BIN capture-pane -t "$session" -p 2>/dev/null || true)

    # Check for rate limit / usage limit messages
    local limit_line
    limit_line=$(echo "$pane_text" | grep -i "out of.*usage\|rate limit\|resets " | tail -1 || true)

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

  if ! session_exists "$session"; then
    log "SKIP $session — session not found"
    ntfy "$agent — not found" "Session $session does not exist. Next try: $(next_run "$agent")"
    return
  fi

  if ! session_idle "$session"; then
    # Also check if session is stuck on a rate limit
    local pane_text
    pane_text=$($TMUX_BIN capture-pane -t "$session" -p 2>/dev/null || true)
    if echo "$pane_text" | grep -qi "out of.*usage\|rate limit"; then
      log "RATE-LIMITED $session — switching backend"
      switch_backend "$session" "$agent"
      sleep 15
      /usr/local/bin/kick-agents.sh "$agent"
      return
    fi
    log "SKIP $session — already working"
    ntfy "$agent — busy" "Still working, skipped kick at $ET_NOW. Next: $(next_run "$agent")"
    return
  fi

  log "KICK $session"
  $TMUX_BIN send-keys -t "$session" "$message"
  $TMUX_BIN send-keys -t "$session" Enter
  ntfy "$agent started" "Kicked at $ET_NOW. Next: $(next_run "$agent")"

  # Background check for rate limit after kick settles
  check_rate_limit "$session" "$agent" 60
}

PULL_INSTRUCTIONS="First: cd /tmp/supervised-agent && git pull --rebase origin main. Re-read your CLAUDE.md for any updated instructions."

SCANNER_MSG="$PULL_INSTRUCTIONS \
Then: Run a full scan pass per your policy (project_scanner_policy.md). \
Oldest-first. Check all 5 repos: kubestellar/console, console-kb, docs, \
console-marketplace, kubestellar-mcp. Ignore all labels EXCEPT: skip any issue/PR with a label containing 'hold'. \
For EVERY open issue that does not already have an active PR, dispatch a background fix agent using the Agent tool with worktrees. \
Do NOT just count issues and stop — your job is to FIX them, not report them. \
Merge AI-authored PRs with green CI. Send ntfy (curl -s -H 'Title: Scanner: <action>' -d '<details>' ntfy.sh/issue-scanner) for every merge and external PR review. \
Log to cron_scan_log.md."

REVIEWER_MSG="$PULL_INSTRUCTIONS \
Then: Run a full reviewer pass per /tmp/supervised-agent/examples/kubestellar/agents/reviewer-CLAUDE.md. \
Check: (A) coverage ≥91%, (B) OAuth code presence, (B.5) CI workflow health sweep, \
(C) release freshness + brew formula + Helm chart appVersion + vllm-d + pok-prod01 \
deploy health, (D) GA4 error watch + adoption digest, (F) post-merge diff scan. \
Print all GA4 tables to this pane. Send ntfy for all findings. Write all results to reviewer_log.md."

ARCHITECT_MSG="$PULL_INSTRUCTIONS \
Then: Run an architect pass per /tmp/supervised-agent/examples/kubestellar/agents/architect-CLAUDE.md. \
Pull main, scan the codebase for refactor or perf improvement opportunities. \
You may work autonomously on refactors and perf as long as you do not break \
the build, touch OAuth, or touch the update system. For new feature ideas, \
open an issue with label architect-idea and wait for operator approval. \
Send ntfy for all plans and PRs. Print your plan to this pane."

OUTREACH_MSG="$PULL_INSTRUCTIONS \
Then: Run an outreach pass per /tmp/supervised-agent/examples/kubestellar/agents/outreacher-CLAUDE.md. \
Your primary objective is increasing organic search results for KubeStellar Console \
using every marketing angle available. Find awesome lists, directories, comparison sites, \
aggregators, community forums, and anywhere else Console should be listed. \
Open PRs and issues to get Console added. Fork under clubanderson account for external PRs. \
Also work on ACMM badge outreach to CNCF projects. \
Send ntfy for all outreach actions. One outreach per project — never spam."

case "$TARGET" in
  scanner)
    kick "issue-scanner" "$SCANNER_MSG" "scanner"
    ;;
  reviewer)
    kick "reviewer" "$REVIEWER_MSG" "reviewer"
    ;;
  architect)
    kick "feature" "$ARCHITECT_MSG" "architect"
    ;;
  outreach)
    kick "outreach" "$OUTREACH_MSG" "outreach"
    ;;
  all)
    kick "issue-scanner" "$SCANNER_MSG" "scanner"
    kick "reviewer" "$REVIEWER_MSG" "reviewer"
    kick "feature" "$ARCHITECT_MSG" "architect"
    kick "outreach" "$OUTREACH_MSG" "outreach"
    ;;
  *)
    echo "Usage: $0 [scanner|reviewer|architect|outreach|all]" >&2
    exit 1
    ;;
esac

log "DONE"
