#!/bin/bash
# contributor-dispatcher.sh — Pipeline stage: report contributor assignment state.
#
# Reads /var/run/hive-metrics/contributors.json (written by dashboard server.js)
# and outputs a summary for the governor and dashboard to consume.
#
# This script is read-only — it does NOT assign tasks. Task assignment happens
# in real-time via the WebSocket server in dashboard/server.js. This script
# only reports the current state for observability.
#
# Output: /var/run/hive-metrics/contributor-assignments.json
#
# Called by: run-pipeline.sh (post-classifier stage)

set -euo pipefail

METRICS_DIR="${HIVE_METRICS_DIR:-/var/run/hive-metrics}"
CONTRIBUTORS_FILE="$METRICS_DIR/contributors.json"
OUTPUT_FILE="$METRICS_DIR/contributor-assignments.json"

if [[ ! -f "$CONTRIBUTORS_FILE" ]]; then
  echo '{"active":0,"assigned":0,"idle":0,"assignments":[],"updated_at":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'"}' > "$OUTPUT_FILE"
  exit 0
fi

python3 -c "
import json, sys
from datetime import datetime

try:
    with open('$CONTRIBUTORS_FILE') as f:
        data = json.load(f)
except Exception:
    data = {'active': []}

active = data.get('active', [])
assigned = [c for c in active if c.get('current_task')]
idle = [c for c in active if not c.get('current_task')]

assignments = []
for c in assigned:
    task = c.get('current_task', {})
    assignments.append({
        'contributor_id': c.get('contributor_id', ''),
        'github_username': c.get('github_username', ''),
        'cli_backend': c.get('cli_backend', ''),
        'trust_tier': c.get('trust_tier', ''),
        'task_id': task.get('task_id', ''),
        'kind': task.get('kind', ''),
        'repo': task.get('repo', ''),
        'number': task.get('number', 0),
    })

result = {
    'active': len(active),
    'assigned': len(assigned),
    'idle': len(idle),
    'assignments': assignments,
    'updated_at': datetime.utcnow().strftime('%Y-%m-%dT%H:%M:%SZ'),
}

print(json.dumps(result, indent=2))
" > "$OUTPUT_FILE"
