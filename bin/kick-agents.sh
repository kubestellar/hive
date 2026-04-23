#!/bin/bash
# kick-agents.sh — fires work orders at the scanner and reviewer tmux sessions.
# Called by systemd timers (or manually). Does NOT require Claude to be running
# as a supervisor — it speaks directly to the named tmux sessions.
#
# Usage:
#   kick-agents.sh scanner   # kick scanner only
#   kick-agents.sh reviewer  # kick reviewer only
#   kick-agents.sh all       # kick both (default)
#
# Systemd timer fires this every 15 min for scanner, every 30 min for reviewer.

set -euo pipefail

TARGET="${1:-all}"
TMUX_BIN="${TMUX_BIN:-tmux}"
LOG="/var/log/kick-agents.log"
TIMESTAMP="$(TZ=America/New_York date '+%Y-%m-%d %H:%M:%S %Z')"

log() { echo "[$TIMESTAMP] $*" | tee -a "$LOG"; }

session_exists() {
  $TMUX_BIN has-session -t "$1" 2>/dev/null
}

session_idle() {
  # Returns 0 (idle) if the pane's last line is the shell prompt (❯ or $)
  local pane
  pane="$($TMUX_BIN capture-pane -t "$1" -p | tail -5)"
  echo "$pane" | grep -qE '^\s*(❯|\$)\s*$'
}

kick() {
  local session="$1"
  local message="$2"

  if ! session_exists "$session"; then
    log "SKIP $session — session not found"
    return
  fi

  # Don't interrupt if the session is actively working (not at idle prompt)
  if ! session_idle "$session"; then
    log "SKIP $session — already working"
    return
  fi

  log "KICK $session"
  $TMUX_BIN send-keys -t "$session" "$message" Enter
}

SCANNER_MSG="Run a full scan pass per your policy (project_scanner_policy.md). \
Oldest-first. Check all 5 repos: kubestellar/console, console-kb, docs, \
console-marketplace, kubestellar-mcp. Dispatch fix agents for open issues \
(skip epics owned by other sessions — check for active PRs first). \
Merge AI-authored PRs with green CI. Log to cron_scan_log.md."

REVIEWER_MSG="Run a full reviewer pass per your policy (project_reviewer_policy.md). \
Check: (A) coverage ≥91%, (B) OAuth code presence, (B.5) CI workflow health sweep, \
(C) release freshness + brew formula + Helm chart appVersion + vllm-d + pok-prod01 \
deploy health, (D) GA4 error watch + adoption digest, (F) post-merge diff scan. \
Write all results to reviewer_log.md."

case "$TARGET" in
  scanner)
    kick "issue-scanner" "$SCANNER_MSG"
    ;;
  reviewer)
    kick "reviewer" "$REVIEWER_MSG"
    ;;
  all)
    kick "issue-scanner" "$SCANNER_MSG"
    kick "reviewer" "$REVIEWER_MSG"
    ;;
  *)
    echo "Usage: $0 [scanner|reviewer|all]" >&2
    exit 1
    ;;
esac

log "DONE"
