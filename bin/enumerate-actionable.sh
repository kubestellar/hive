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
#   - External contributor issues missing a commit SHA in the body
#     (auto-labeled hold, comment posted asking for the SHA)

set -euo pipefail

OUTPUT_FILE="/var/run/hive-metrics/actionable.json"
TMP_FILE="${OUTPUT_FILE}.tmp"
LOG="/var/log/kick-agents.log"

# Read repos from hive-project.yaml (single source of truth)
PROJECT_YAML="${HIVE_PROJECT_YAML:-/etc/hive/hive-project.yaml}"
if [ ! -f "$PROJECT_YAML" ]; then
  PROJECT_YAML="$(find "$(dirname "$(dirname "$0")")/examples" -name 'hive-project.yaml' -type f 2>/dev/null | head -1)"
fi

# Source project config for AI author and primary repo
# shellcheck source=hive-config.sh
source "$(dirname "$0")/hive-config.sh" 2>/dev/null || source /usr/local/bin/hive-config.sh 2>/dev/null || true

if [ -f "$PROJECT_YAML" ]; then
  mapfile -t REPOS < <(python3 -c "
import yaml, sys
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
for r in cfg.get('project', {}).get('repos', []):
    print(r)
" "$PROJECT_YAML" 2>/dev/null)
fi

if [ ${#REPOS[@]} -eq 0 ]; then
  echo "ERROR: no repos found in $PROJECT_YAML" >&2
  exit 1
fi

ISSUE_LIMIT=50
PR_LIMIT=30

log() { echo "[$(date -Is)] ENUM $*" >> "$LOG"; }

log "START — scanning ${#REPOS[@]} repos"

issues_tmp=$(mktemp)
prs_tmp=$(mktemp)
trap 'rm -f "$issues_tmp" "$prs_tmp"' EXIT

# --- Fetch issues and PRs sequentially across all repos ---
for repo in "${REPOS[@]}"; do
  /usr/bin/gh api "repos/${repo}/issues?state=open&per_page=${ISSUE_LIMIT}&sort=created&direction=asc" \
    --jq "[.[] | select(.pull_request == null) | {
      repo: \"${repo}\",
      number: .number,
      title: .title,
      body: (.body // \"\"),
      author: .user.login,
      author_type: .user.type,
      created_at: .created_at,
      labels: [.labels[].name],
      assignees: [.assignees[].login],
      url: .html_url
    }]" >> "$issues_tmp" 2>/dev/null || echo "[]" >> "$issues_tmp"

  /usr/bin/gh api "repos/${repo}/pulls?state=open&per_page=${PR_LIMIT}&sort=created&direction=asc" \
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
    files=$(/usr/bin/gh api "repos/${repo}/pulls/${num}/files" --jq '.[].filename' 2>/dev/null || echo "")
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

# --- SHA enforcement for external contributor issues ---
# Internal authors: their issues don't need a SHA (auto-generated issues, maintainer issues)
INTERNAL_AUTHORS="${PROJECT_AI_AUTHOR:-} copilot-swe-agent[bot] github-actions[bot] dependabot[bot]"
SHA_HOLD_MARKER="/var/run/hive-metrics/sha_hold_posted"
mkdir -p "$(dirname "$SHA_HOLD_MARKER")"

sha_result=$(echo "$all_issues" | python3 -c "
import json, sys, re

issues = json.load(sys.stdin)
internal = set(sys.argv[1].split())

SHA_PATTERN = re.compile(r'[0-9a-f]{7,40}\b')

missing_sha = []
kept = []

for i in issues:
    author = i.get('author', '')
    author_type = i.get('author_type', 'User')
    if author in internal or author_type == 'Bot':
        kept.append(i)
        continue
    body = i.get('body', '') or ''
    if SHA_PATTERN.search(body):
        kept.append(i)
    else:
        missing_sha.append(i)

print(json.dumps({'kept': kept, 'missing_sha': missing_sha}))
" "$INTERNAL_AUTHORS" 2>/dev/null || echo '{"kept":[],"missing_sha":[]}')

# For issues missing SHA: label hold + post comment (only once per issue)
missing_sha_issues=$(echo "$sha_result" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for i in d.get('missing_sha', []):
    print(f\"{i['repo']}:{i['number']}\")
" 2>/dev/null)

if [ -n "$missing_sha_issues" ]; then
  for entry in $missing_sha_issues; do
    repo="${entry%%:*}"
    num="${entry##*:}"
    marker_file="${SHA_HOLD_MARKER}_${repo//\//_}_${num}"
    if [ ! -f "$marker_file" ]; then
      gh issue edit "$num" --repo "$repo" --add-label "hold" --remove-label "kind/bug" 2>/dev/null || true
      gh issue comment "$num" --repo "$repo" --body "$(cat <<COMMENT
Thanks for filing this issue! To help us reproduce and investigate, could you please include the **commit SHA** of the build you're running.

You can find it by:
- **Git**: \`git rev-parse HEAD\` in your repo checkout
- **Git log**: \`git log --oneline -1\`
- **GitHub CLI**: \`/usr/bin/gh api repos/${repo}/commits/main --jq .sha\`
- **Console UI**: Check the build version/commit hash in the bottom-right footer

We've put this issue on hold until we can confirm which version it was filed against. Once you add the SHA, we'll pick it back up right away.
COMMENT
)" 2>/dev/null || true
      touch "$marker_file"
      log "SHA-HOLD: ${repo}#${num} — external contributor issue missing commit SHA, labeled hold"
    fi
  done
fi

# --- Re-check previously SHA-held issues: if SHA was added, unhold them ---
for marker_file in "${SHA_HOLD_MARKER}"_*; do
  [ -f "$marker_file" ] || continue
  # Extract repo and number from marker filename: sha_hold_posted_org_repo_NUM
  marker_base=$(basename "$marker_file")
  num="${marker_base##*_}"
  # Reconstruct repo from marker (sha_hold_posted_org_repo_NUM → org/repo)
  mid="${marker_base#sha_hold_posted_}"
  mid="${mid%_${num}}"
  repo="${mid/_//}"
  # Fetch current issue body and check for SHA
  body=$(/usr/bin/gh api "repos/${repo}/issues/${num}" --jq '.body // ""' 2>/dev/null || echo "")
  has_sha=$(echo "$body" | python3 -c "
import sys, re
SHA_PATTERN = re.compile(r'[0-9a-f]{7,40}\b')
print('yes' if SHA_PATTERN.search(sys.stdin.read()) else 'no')
" 2>/dev/null || echo "no")
  if [ "$has_sha" = "yes" ]; then
    gh issue edit "$num" --repo "$repo" --remove-label "hold" --add-label "kind/bug" 2>/dev/null || true
    rm -f "$marker_file"
    log "SHA-UNHOLD: ${repo}#${num} — SHA found in body, removed hold, restored kind/bug"
  fi
done

# Use only the kept issues (SHA-verified or internal)
all_issues=$(echo "$sha_result" | python3 -c "import json,sys; print(json.dumps(json.load(sys.stdin)['kept']))" 2>/dev/null || echo "[]")

# --- Build final output ---
issue_count=$(echo "$all_issues" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)
pr_count=$(echo "$all_prs" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)

python3 -c "
import json, os, sys
from datetime import datetime, timezone

issues = json.loads(sys.argv[1])
prs = json.loads(sys.argv[2])
primary_repo = sys.argv[3] if len(sys.argv) > 3 else os.environ.get('PROJECT_PRIMARY_REPO', '')

now = datetime.now(timezone.utc)

# Compute SLA status for issues (minutes since creation)
for i in issues:
    try:
        created = datetime.fromisoformat(i['created_at'].replace('Z', '+00:00'))
        i['age_minutes'] = int((now - created).total_seconds() / 60)
    except:
        i['age_minutes'] = 0

# Strip body from output (used for SHA check only, too large to keep)
for i in issues:
    i.pop('body', None)
    i.pop('author_type', None)

SLA_MINUTES = 30
sla_violations = [i for i in issues if i.get('age_minutes', 0) > SLA_MINUTES and i.get('repo') == primary_repo]

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
        'drafts': True,
        'external_issues_missing_sha': True
    }
}
print(json.dumps(result, indent=2))
" "$all_issues" "$all_prs" "${PROJECT_PRIMARY_REPO:-}" > "$TMP_FILE" 2>/dev/null

mv "$TMP_FILE" "$OUTPUT_FILE"

log "DONE — $issue_count actionable issues, $pr_count actionable PRs"
