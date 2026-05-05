#!/bin/bash
# outreach-tracker.sh — Monitor: track outreach PRs opened/merged on external repos.
# Writes /var/run/hive-metrics/outreach-prs.json.
# Outreach agent reads this for accurate counts instead of running its own searches.
#
# Config: hive-project.yaml → outreach (author from project.ai_author)
# Category: monitor (runs pre-kick, detects external state)

set -euo pipefail

OUTPUT_FILE="/var/run/hive-metrics/outreach-prs.json"
TMP_FILE="${OUTPUT_FILE}.tmp"
LOG="/var/log/kick-agents.log"
REAL_GH="/usr/bin/gh"

PROJECT_YAML="${HIVE_PROJECT_YAML:-/etc/hive/hive-project.yaml}"
if [ ! -f "$PROJECT_YAML" ]; then
  PROJECT_YAML="$(find "$(dirname "$(dirname "$0")")/examples" -name 'hive-project.yaml' -type f 2>/dev/null | head -1)"
fi

log() { echo "[$(date -Is)] OUTREACH-TRACK $*" >> "$LOG"; }

# Read config
CONFIG=$(python3 -c "
import yaml, sys, json
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
result = {
    'ai_author': cfg.get('project', {}).get('ai_author', ''),
    'org': cfg.get('project', {}).get('org', ''),
    'repos': cfg.get('project', {}).get('repos', []),
    'target_placements': cfg.get('outreach', {}).get('target_placements', 0),
}
print(json.dumps(result))
" "$PROJECT_YAML" 2>/dev/null || echo '{}')

AI_AUTHOR=$(echo "$CONFIG" | python3 -c "import json,sys; print(json.load(sys.stdin).get('ai_author',''))" 2>/dev/null)
ORG=$(echo "$CONFIG" | python3 -c "import json,sys; print(json.load(sys.stdin).get('org',''))" 2>/dev/null)
TARGET=$(echo "$CONFIG" | python3 -c "import json,sys; print(json.load(sys.stdin).get('target_placements',0))" 2>/dev/null)
INTERNAL_REPOS=$(echo "$CONFIG" | python3 -c "import json,sys; print(' '.join(json.load(sys.stdin).get('repos',[])))" 2>/dev/null)

# Record pause state alongside tracker run
_OT_PAUSED_GOV=false; [[ -f "/var/run/kick-governor/paused_outreach" ]] && _OT_PAUSED_GOV=true
_OT_PAUSED_OP=false; [[ -f "/var/run/kick-governor/operator_paused_outreach" ]] && _OT_PAUSED_OP=true
log "START — tracking outreach PRs by $AI_AUTHOR outside $ORG (paused_gov=$_OT_PAUSED_GOV paused_op=$_OT_PAUSED_OP)"
printf '{"ts":"%s","agent":"outreach","action":"TRACK","reason":"outreach-tracker","caller":"outreach-tracker","paused_governor":%s,"paused_operator":%s,"paused_etc":false}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S+00:00)" "$_OT_PAUSED_GOV" "$_OT_PAUSED_OP" >> "/var/log/kick-audit.jsonl"

# Search for all open PRs by the AI author on external repos
open_prs=$($REAL_GH search prs --author "$AI_AUTHOR" --state open --limit 200 \
  --json repository,number,title,createdAt,url 2>/dev/null || echo "[]")

# Search for merged PRs by the AI author (last 90 days for accurate totals)
merged_prs=$($REAL_GH search prs --author "$AI_AUTHOR" --state closed --merged --limit 200 \
  --json repository,number,title,createdAt,closedAt,url 2>/dev/null || echo "[]")

# Filter to external repos only, classify, and count
python3 -c "
import json, sys
from datetime import datetime, timezone

open_prs = json.loads(sys.argv[1])
merged_prs = json.loads(sys.argv[2])
org = sys.argv[3]
internal_repos = set(sys.argv[4].split())
target = int(sys.argv[5]) if sys.argv[5] else 0

now = datetime.now(timezone.utc)

def is_external(pr):
    repo_name = pr.get('repository', {}).get('nameWithOwner', '') if isinstance(pr.get('repository'), dict) else ''
    if not repo_name:
        return False
    return not repo_name.startswith(f'{org}/') and repo_name not in internal_repos

def is_adopters_pr(pr):
    title = (pr.get('title', '') or '').lower()
    return 'adopter' in title or 'adopters' in title

# Filter external only
ext_open = [p for p in open_prs if is_external(p)]
ext_merged = [p for p in merged_prs if is_external(p)]

# Classify
adopters_open = [p for p in ext_open if is_adopters_pr(p)]
adopters_merged = [p for p in ext_merged if is_adopters_pr(p)]
other_open = [p for p in ext_open if not is_adopters_pr(p)]
other_merged = [p for p in ext_merged if not is_adopters_pr(p)]

# Unique orgs with merged PRs (placements)
merged_orgs = set()
for p in ext_merged:
    repo = p.get('repository', {}).get('nameWithOwner', '') if isinstance(p.get('repository'), dict) else ''
    if repo:
        merged_orgs.add(repo.split('/')[0])

# One-PR-per-org check: orgs with open PRs
open_orgs = {}
for p in ext_open:
    repo = p.get('repository', {}).get('nameWithOwner', '') if isinstance(p.get('repository'), dict) else ''
    if repo:
        org_name = repo.split('/')[0]
        if org_name not in open_orgs:
            open_orgs[org_name] = []
        open_orgs[org_name].append({
            'repo': repo,
            'number': p.get('number'),
            'title': p.get('title', '')[:80],
            'url': p.get('url', '')
        })

# Flag orgs with >1 open PR (violates one-per-org rule)
multi_pr_orgs = {k: v for k, v in open_orgs.items() if len(v) > 1}

def fmt_pr(p):
    repo = p.get('repository', {}).get('nameWithOwner', '') if isinstance(p.get('repository'), dict) else ''
    return {
        'repo': repo,
        'number': p.get('number'),
        'title': (p.get('title', '') or '')[:80],
        'url': p.get('url', ''),
        'created_at': p.get('createdAt', ''),
        'is_adopters': is_adopters_pr(p)
    }

result = {
    'generated_at': now.isoformat(),
    'counts': {
        'open_total': len(ext_open),
        'open_adopters': len(adopters_open),
        'open_other': len(other_open),
        'merged_total': len(ext_merged),
        'merged_adopters': len(adopters_merged),
        'merged_other': len(other_merged),
        'unique_orgs_merged': len(merged_orgs),
        'target_placements': target,
        'progress_pct': round(len(merged_orgs) / target * 100, 1) if target > 0 else 0
    },
    'one_pr_per_org_violations': {k: v for k, v in multi_pr_orgs.items()},
    'open_prs': [fmt_pr(p) for p in ext_open],
    'merged_prs': [fmt_pr(p) for p in ext_merged[:50]],
    'blocked_orgs': list(open_orgs.keys()),
}

print(json.dumps(result, indent=2))
" "$open_prs" "$merged_prs" "$ORG" "$INTERNAL_REPOS" "$TARGET" > "$TMP_FILE"

mv "$TMP_FILE" "$OUTPUT_FILE"

summary=$(python3 -c "
import json
d = json.load(open('$OUTPUT_FILE'))
c = d['counts']
v = len(d.get('one_pr_per_org_violations', {}))
print(f\"{c['open_total']} open ({c['open_adopters']} adopters), {c['merged_total']} merged, {c['unique_orgs_merged']}/{c['target_placements']} placements ({c['progress_pct']}%)\")
if v: print(f'  ⚠ {v} orgs have >1 open PR (one-per-org violation)')
" 2>/dev/null)

log "DONE — $summary"
echo "$summary"
