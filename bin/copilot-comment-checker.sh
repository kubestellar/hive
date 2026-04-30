#!/bin/bash
# copilot-comment-checker.sh — Monitor: pre-fetch unaddressed Copilot review comments.
# Writes /var/run/hive-metrics/copilot-comments.json.
# Reviewer reads this instead of scanning PRs itself.
#
# Config: hive-project.yaml → classification.copilot_check
# Category: monitor (runs pre-kick, detects external state)

set -euo pipefail

OUTPUT_FILE="/var/run/hive-metrics/copilot-comments.json"
TMP_FILE="${OUTPUT_FILE}.tmp"
LOG="/var/log/kick-agents.log"
REAL_GH="/usr/bin/gh"

PROJECT_YAML="${HIVE_PROJECT_YAML:-/etc/hive/hive-project.yaml}"
if [ ! -f "$PROJECT_YAML" ]; then
  PROJECT_YAML="$(dirname "$(dirname "$0")")/examples/kubestellar/hive-project.yaml"
fi

log() { echo "[$(date -Is)] COPILOT-CHECK $*" >> "$LOG"; }

# Read config
CONFIG=$(python3 -c "
import yaml, sys, json
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
result = {
    'repos': cfg.get('project', {}).get('repos', []),
    'copilot': cfg.get('classification', {}).get('copilot_check', {})
}
print(json.dumps(result))
" "$PROJECT_YAML" 2>/dev/null || echo '{"repos":[],"copilot":{}}')

REPOS=$(echo "$CONFIG" | python3 -c "import json,sys; [print(r) for r in json.load(sys.stdin)['repos']]" 2>/dev/null)

if [ -z "$REPOS" ]; then
  echo '{"error":"no repos","total_unaddressed":0,"comments":[]}' > "$OUTPUT_FILE"
  exit 0
fi

LOOKBACK_DAYS=$(echo "$CONFIG" | python3 -c "import json,sys; print(json.load(sys.stdin)['copilot'].get('lookback_days', 1))" 2>/dev/null || echo 1)

log "START — scanning for Copilot comments (${LOOKBACK_DAYS}d lookback)"

SINCE_DATE=$(date -u -v-${LOOKBACK_DAYS}d '+%Y-%m-%d' 2>/dev/null || date -u -d "${LOOKBACK_DAYS} days ago" '+%Y-%m-%d' 2>/dev/null || date -u '+%Y-%m-%d')

all_comments_tmp=$(mktemp)
trap 'rm -f "$all_comments_tmp"' EXIT

SEVERITY_KEYWORDS_JSON=$(echo "$CONFIG" | python3 -c "
import json, sys
copilot = json.load(sys.stdin)['copilot']
kw = copilot.get('severity_keywords', {
    'high': ['security', 'vulnerability', 'injection', 'xss', 'sql', 'auth', 'credential', 'secret'],
    'medium': ['null', 'undefined', 'error', 'exception', 'crash', 'race', 'deadlock', 'memory', 'leak'],
    'low': ['style', 'naming', 'format', 'whitespace', 'comment', 'typo', 'nit', 'minor']
})
print(json.dumps(kw))
" 2>/dev/null || echo '{}')

for repo in $REPOS; do
  merged_prs=$($REAL_GH api "repos/${repo}/pulls?state=closed&sort=updated&direction=desc&per_page=30" \
    --jq "[.[] | select(.merged_at != null and .merged_at >= \"${SINCE_DATE}\") | {number: .number, title: .title, merged_at: .merged_at}]" 2>/dev/null || echo "[]")

  pr_numbers=$(echo "$merged_prs" | python3 -c "
import json, sys
prs = json.load(sys.stdin)
for p in prs:
    print(p['number'])
" 2>/dev/null)

  [ -z "$pr_numbers" ] && continue

  for pr_num in $pr_numbers; do
    comments=$($REAL_GH api "repos/${repo}/pulls/${pr_num}/comments" \
      --jq "[.[] | select(.user.login == \"copilot-swe-agent[bot]\") | {
        id: .id,
        path: .path,
        line: .line,
        body: .body,
        author: .user.login,
        created_at: .created_at,
        in_reply_to_id: .in_reply_to_id
      }]" 2>/dev/null || echo "[]")

    echo "$comments" | python3 -c "
import json, sys

repo = '${repo}'
pr_num = ${pr_num}
comments = json.load(sys.stdin)
severity_keywords = json.loads(sys.argv[1])

for c in comments:
    if c.get('in_reply_to_id'):
        continue
    body_lower = (c.get('body', '') or '').lower()
    severity = 'medium'
    for level in ['high', 'medium', 'low']:
        keywords = severity_keywords.get(level, [])
        if any(kw in body_lower for kw in keywords):
            severity = level
            break

    result = {
        'repo': repo,
        'pr_number': pr_num,
        'comment_id': c['id'],
        'file': c.get('path', ''),
        'line': c.get('line'),
        'body': (c.get('body', '') or '')[:200],
        'severity': severity,
        'created_at': c.get('created_at', '')
    }
    print(json.dumps(result))
" "$SEVERITY_KEYWORDS_JSON" >> "$all_comments_tmp" 2>/dev/null || true
  done
done

python3 -c "
import json, sys
from datetime import datetime, timezone

comments = []
with open(sys.argv[1]) as f:
    for line in f:
        line = line.strip()
        if line:
            try:
                comments.append(json.loads(line))
            except json.JSONDecodeError:
                pass

by_severity = {'high': 0, 'medium': 0, 'low': 0}
for c in comments:
    sev = c.get('severity', 'medium')
    by_severity[sev] = by_severity.get(sev, 0) + 1

prs_with_comments = len(set(f\"{c['repo']}#{c['pr_number']}\" for c in comments))

result = {
    'generated_at': datetime.now(timezone.utc).isoformat(),
    'total_unaddressed': len(comments),
    'prs_with_comments': prs_with_comments,
    'by_severity': by_severity,
    'comments': sorted(comments, key=lambda c: {'high': 0, 'medium': 1, 'low': 2}.get(c.get('severity', 'medium'), 1))
}
print(json.dumps(result, indent=2))
" "$all_comments_tmp" > "$TMP_FILE"

mv "$TMP_FILE" "$OUTPUT_FILE"

total=$(python3 -c "import json; d=json.load(open('$OUTPUT_FILE')); print(d['total_unaddressed'])" 2>/dev/null || echo 0)
log "DONE — $total unaddressed Copilot comments found"
