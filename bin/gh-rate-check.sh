#!/bin/bash
# gh-rate-check.sh — Scans agent tmux panes for GitHub API rate limit messages.
# Writes alerts to /var/run/hive-metrics/gh_rate_limits.json and sends ntfy notifications.
# Called periodically (e.g., every 60s via systemd timer or cron).
#
# IMPORTANT: This script detects GITHUB API rate limits only.
# Claude/Copilot CLI usage limits ("You're out of extra usage", "resets Xam/pm")
# are handled separately by kick-agents.sh and must NOT be confused with GH API limits.

set -euo pipefail

METRICS_FILE="/var/run/hive-metrics/gh_rate_limits.json"
NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"
NTFY_TOPIC="${NTFY_TOPIC:-hive}"
TMUX_BIN="${TMUX_BIN:-tmux}"
TTL_SECONDS=3600  # 1 hour — alerts older than this are pruned

# Agent name -> tmux session name
declare -A AGENT_SESSIONS=(
  [scanner]=scanner
  [reviewer]=reviewer
  [architect]=architect
  [outreach]=outreach
)

# GitHub API rate limit patterns — these indicate the GitHub REST/GraphQL API
# is throttling the gh CLI. These should NOT trigger an agent restart.
GH_RATE_PATTERNS='API rate limit exceeded|secondary rate limit|rate limit|403.*rate|You have exceeded|retry-after|gh: Resource not accessible'

# Claude/Copilot CLI patterns to EXCLUDE — these are handled by kick-agents.sh
# and indicate the AI backend is exhausted, not the GitHub API.
CLI_EXCLUDE_PATTERNS="You.re out of extra usage|out of extra usage|extra usage.*resets|resets [0-9]+(:[0-9]+)?[aApP][mM]"

now_epoch=$(date +%s)
now_iso=$(date -Is)

# Load existing alerts (or start fresh)
if [ -f "$METRICS_FILE" ]; then
  existing=$(cat "$METRICS_FILE" 2>/dev/null || echo '{"alerts":[]}')
else
  existing='{"alerts":[]}'
fi

# Prune expired alerts and process with python3
new_alerts=$(echo "$existing" | python3 -c "
import json, sys
data = json.load(sys.stdin)
now = $now_epoch
ttl = $TTL_SECONDS
alerts = [a for a in data.get('alerts', [])
          if now - a.get('detected_epoch', 0) < ttl]
print(json.dumps(alerts))
" 2>/dev/null || echo '[]')

for agent in "${!AGENT_SESSIONS[@]}"; do
  session="${AGENT_SESSIONS[$agent]}"

  # Skip if session does not exist
  if ! $TMUX_BIN has-session -t "$session" 2>/dev/null; then
    continue
  fi

  # Capture last 100 lines of the pane
  pane_text=$($TMUX_BIN capture-pane -t "$session" -p -S -100 2>/dev/null || true)
  [ -z "$pane_text" ] && continue

  # First, filter OUT any lines that match Claude/Copilot CLI patterns
  filtered_text=$(echo "$pane_text" | grep -viE "$CLI_EXCLUDE_PATTERNS" || true)
  [ -z "$filtered_text" ] && continue

  # Now check the filtered text for GitHub API rate limit patterns
  match_line=$(echo "$filtered_text" | grep -iE "$GH_RATE_PATTERNS" | tail -1 || true)
  [ -z "$match_line" ] && continue

  # Trim the match line for display (max 200 chars, strip leading whitespace)
  match_msg=$(echo "$match_line" | sed 's/^[[:space:]]*//' | head -c 200)

  # Check if we already have an active (non-expired) alert for this agent
  already_alerted=$(echo "$new_alerts" | python3 -c "
import json, sys
alerts = json.load(sys.stdin)
agent = sys.argv[1]
found = any(a['agent'] == agent for a in alerts)
print('yes' if found else 'no')
" "$agent" 2>/dev/null || echo 'no')

  if [ "$already_alerted" = "yes" ]; then
    continue
  fi

  # Add new alert
  new_alerts=$(echo "$new_alerts" | python3 -c "
import json, sys
alerts = json.load(sys.stdin)
alerts.append({
    'agent': sys.argv[1],
    'detected_at': sys.argv[2],
    'detected_epoch': int(sys.argv[3]),
    'message': sys.argv[4][:200],
    'ttl_seconds': int(sys.argv[5])
})
print(json.dumps(alerts))
" "$agent" "$now_iso" "$now_epoch" "$match_msg" "$TTL_SECONDS" 2>/dev/null || echo "$new_alerts")

  # Send ntfy notification
  curl -s \
    -H "Title: GH Rate Limit: $agent" \
    -H "Priority: high" \
    -H "Tags: warning" \
    -d "$match_msg" \
    "$NTFY_SERVER/$NTFY_TOPIC" >/dev/null 2>&1 || true

  echo "[$(date -Is)] GH-RATE-LIMIT $agent — $match_msg" >> /var/log/kick-agents.log
done

# Write final alerts file
echo "$new_alerts" | python3 -c "
import json, sys
alerts = json.load(sys.stdin)
result = {'alerts': alerts, 'updated_at': sys.argv[1]}
print(json.dumps(result, indent=2))
" "$now_iso" > "$METRICS_FILE" 2>/dev/null || echo "{\"alerts\":$new_alerts,\"updated_at\":\"$now_iso\"}" > "$METRICS_FILE"
