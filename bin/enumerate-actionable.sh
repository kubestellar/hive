#!/bin/bash
# enumerate-actionable.sh — Canonical enumerator for actionable issues and PRs.
# Runs before each kick cycle. Writes /var/run/hive-metrics/actionable.json.
# Agents read this file instead of running their own gh issue/pr list queries.
#
# Exclusion rules (structural, not advisory):
#   - Issues/PRs with any label containing "hold" (hold, on-hold, hold/review)
#   - Issues with labels: do-not-merge, auto-qa-tuning-report, or starting with "LFX"
#   - PRs that modify ADOPTERS.md / ADOPTERS.MD (checked via file list)
#   - Draft PRs (isDraft=true)

set -euo pipefail

OUTPUT_FILE="/var/run/hive-metrics/actionable.json"
TMP_FILE="${OUTPUT_FILE}.tmp"
LOG="/var/log/kick-agents.log"

REPOS=(
  kubestellar/console
  kubestellar/docs
  kubestellar/console-kb
  kubestellar/kubestellar-mcp
)

ISSUE_LIMIT=50
PR_LIMIT=30

log() { echo "[$(date -Is)] ENUM $*" >> "$LOG"; }

log "START — scanning ${#REPOS[@]} repos"

issues_tmp=$(mktemp)
prs_tmp=$(mktemp)
trap 'rm -f "$issues_tmp" "$prs_tmp"' EXIT

# --- Fetch issues and PRs sequentially across all repos ---
for repo in "${REPOS[@]}"; do
  gh api "repos/${repo}/issues?state=open&per_page=${ISSUE_LIMIT}&sort=created&direction=asc" \
    --jq "[.[] | select(.pull_request == null) | {
      repo: \"${repo}\",
      number: .number,
      title: .title,
      created_at: .created_at,
      labels: [.labels[].name],
      assignees: [.assignees[].login],
      url: .html_url
    }]" >> "$issues_tmp" 2>/dev/null || echo "[]" >> "$issues_tmp"

  gh api "repos/${repo}/pulls?state=open&per_page=${PR_LIMIT}&sort=created&direction=asc" \
    --jq "[.[] | {
      repo: \"${repo}\",
      number: .number,
      title: .title,
      created_at: .created_at,
      labels: [.labels[].name],
      author: .user.login,
      draft: .draft,
      url: .html_url
    }]" >> "$prs_tmp" 2>/dev/null || echo "[]" >> "$prs_tmp"
done

# --- Filter issues with python3 ---
all_issues=$(cat "$issues_tmp" | python3 -c "
import json, sys

raw = sys.stdin.read()
# Parse multiple JSON arrays concatenated together
arrays = []
decoder = json.JSONDecoder()
pos = 0
while pos < len(raw):
    raw_stripped = raw[pos:].lstrip()
    if not raw_stripped:
        break
    pos = len(raw) - len(raw_stripped)
    try:
        obj, end = decoder.raw_decode(raw, pos)
        arrays.extend(obj if isinstance(obj, list) else [obj])
        pos += end
    except json.JSONDecodeError:
        break

HOLD_SUBSTRINGS = ['hold']
EXCLUDED_LABELS = {'do-not-merge', 'auto-qa-tuning-report'}
EXCLUDED_PREFIXES = ('LFX',)

def is_excluded(labels):
    for l in labels:
        ll = l.lower()
        for h in HOLD_SUBSTRINGS:
            if h in ll:
                return True
        if l in EXCLUDED_LABELS:
            return True
        for p in EXCLUDED_PREFIXES:
            if l.startswith(p):
                return True
    return False

filtered = [i for i in arrays if not is_excluded(i.get('labels', []))]
filtered.sort(key=lambda x: x.get('created_at', ''))
print(json.dumps(filtered))
" 2>/dev/null || echo "[]")

# --- Filter PRs: exclude hold-labeled, drafts, and ADOPTERS file PRs ---
# First pass: filter by labels and draft status
pre_filtered_prs=$(cat "$prs_tmp" | python3 -c "
import json, sys

raw = sys.stdin.read()
arrays = []
decoder = json.JSONDecoder()
pos = 0
while pos < len(raw):
    raw_stripped = raw[pos:].lstrip()
    if not raw_stripped:
        break
    pos = len(raw) - len(raw_stripped)
    try:
        obj, end = decoder.raw_decode(raw, pos)
        arrays.extend(obj if isinstance(obj, list) else [obj])
        pos += end
    except json.JSONDecodeError:
        break

HOLD_SUBSTRINGS = ['hold']

def has_hold(labels):
    for l in labels:
        if any(h in l.lower() for h in HOLD_SUBSTRINGS):
            return True
    return False

filtered = [p for p in arrays if not p.get('draft', False) and not has_hold(p.get('labels', []))]
filtered.sort(key=lambda x: x.get('created_at', ''))
print(json.dumps(filtered))
" 2>/dev/null || echo "[]")

# Second pass: check file lists for ADOPTERS PRs (parallel, only for remaining PRs)
adopters_tmp=$(mktemp)
trap 'rm -f "$issues_tmp" "$prs_tmp" "$adopters_tmp"' EXIT

pr_numbers=$(echo "$pre_filtered_prs" | python3 -c "
import json, sys
prs = json.load(sys.stdin)
for p in prs:
    print(f\"{p['repo']}:{p['number']}\")
" 2>/dev/null)

# Check each PR's files for ADOPTERS in parallel
if [ -n "$pr_numbers" ]; then
  for entry in $pr_numbers; do
    repo="${entry%%:*}"
    num="${entry##*:}"
    files=$(gh api "repos/${repo}/pulls/${num}/files" --jq '.[].filename' 2>/dev/null || echo "")
    if echo "$files" | grep -qi 'adopters'; then
      echo "$num" >> "$adopters_tmp"
    fi
  done
fi

adopters_prs=""
[ -f "$adopters_tmp" ] && adopters_prs=$(cat "$adopters_tmp" | tr '\n' ',')

all_prs=$(echo "$pre_filtered_prs" | python3 -c "
import json, sys
prs = json.load(sys.stdin)
exclude_nums = set()
raw_exclude = sys.argv[1] if len(sys.argv) > 1 else ''
for n in raw_exclude.split(','):
    n = n.strip()
    if n:
        try:
            exclude_nums.add(int(n))
        except ValueError:
            pass
filtered = [p for p in prs if p['number'] not in exclude_nums]
print(json.dumps(filtered))
" "$adopters_prs" 2>/dev/null || echo "[]")

# --- Build final output ---
issue_count=$(echo "$all_issues" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)
pr_count=$(echo "$all_prs" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)

python3 -c "
import json, sys
from datetime import datetime, timezone

issues = json.loads(sys.argv[1])
prs = json.loads(sys.argv[2])

now = datetime.now(timezone.utc)

# Compute SLA status for issues (minutes since creation)
for i in issues:
    try:
        created = datetime.fromisoformat(i['created_at'].replace('Z', '+00:00'))
        i['age_minutes'] = int((now - created).total_seconds() / 60)
    except:
        i['age_minutes'] = 0

SLA_MINUTES = 30
sla_violations = [i for i in issues if i.get('age_minutes', 0) > SLA_MINUTES and i.get('repo') == 'kubestellar/console']

result = {
    'generated_at': now.isoformat(),
    'issues': {
        'count': len(issues),
        'items': issues,
        'sla_violations': len(sla_violations)
    },
    'prs': {
        'count': len(prs),
        'items': prs
    },
    'exclusions': {
        'labels': ['hold', 'on-hold', 'hold/review', 'do-not-merge', 'auto-qa-tuning-report', 'LFX*'],
        'files': ['ADOPTERS.md', 'ADOPTERS.MD'],
        'drafts': True
    }
}
print(json.dumps(result, indent=2))
" "$all_issues" "$all_prs" > "$TMP_FILE" 2>/dev/null

mv "$TMP_FILE" "$OUTPUT_FILE"

log "DONE — $issue_count actionable issues, $pr_count actionable PRs"
