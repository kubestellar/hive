#!/bin/bash
# kick-agents.sh — fires work orders at the scanner, reviewer, and architect tmux sessions.
# Called by systemd timers (or manually). Does NOT require Claude to be running
# as a supervisor — it speaks directly to the named tmux sessions.
#
# Usage:
#   kick-agents.sh scanner    # kick scanner only
#   kick-agents.sh reviewer   # kick reviewer only
#   kick-agents.sh architect  # kick architect only
#   kick-agents.sh all        # kick all three (default)
#
# Systemd timer fires this every 15 min for scanner, every 30 min for reviewer,
# every 60 min for architect.

set -euo pipefail

TARGET="${1:-all}"
TMUX_BIN="${TMUX_BIN:-tmux}"
LOG="/var/log/kick-agents.log"
TIMESTAMP="$(TZ=America/New_York date '+%Y-%m-%d %H:%M:%S %Z')"
ET_NOW="$(TZ=America/New_York date '+%I:%M %p ET')"
NTFY_TOPIC="ntfy.sh/issue-scanner"

log() { echo "[$TIMESTAMP] $*" | tee -a "$LOG"; }
ntfy() { curl -s -H "Title: $1" -d "$2" "$NTFY_TOPIC" > /dev/null 2>&1 || true; }

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
  esac
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
    log "SKIP $session — already working"
    ntfy "$agent — busy" "Still working, skipped kick at $ET_NOW. Next: $(next_run "$agent")"
    return
  fi

  log "KICK $session"
  $TMUX_BIN send-keys -t "$session" "$message"
  $TMUX_BIN send-keys -t "$session" Enter
  ntfy "$agent started" "Kicked at $ET_NOW. Next: $(next_run "$agent")"
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
Print all GA4 tables to this pane. Write all results to reviewer_log.md."

ARCHITECT_MSG="Run an architect pass per your CLAUDE.md at \
/tmp/supervised-agent/examples/kubestellar/agents/architect-CLAUDE.md. \
Pull main, scan the codebase for refactor or perf improvement opportunities. \
You may work autonomously on refactors and perf as long as you do not break \
the build, touch OAuth, or touch the update system. For new feature ideas, \
open an issue with label architect-idea and wait for operator approval. \
Print your plan to this pane."

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
  all)
    kick "issue-scanner" "$SCANNER_MSG" "scanner"
    kick "reviewer" "$REVIEWER_MSG" "reviewer"
    kick "feature" "$ARCHITECT_MSG" "architect"
    ;;
  *)
    echo "Usage: $0 [scanner|reviewer|architect|all]" >&2
    exit 1
    ;;
esac

log "DONE"
