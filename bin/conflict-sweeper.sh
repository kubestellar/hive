#!/bin/bash
# conflict-sweeper.sh — Rebase or close CONFLICTING PRs.
# Runs as a pre-kick pipeline stage after merge-gate.sh.
# For each open AI-authored PR with mergeable=CONFLICTING:
#   1. Clone/worktree the repo, fetch the PR branch
#   2. Attempt rebase onto main
#   3. If clean rebase: force-push (CI re-runs, merge-gate picks it up next cycle)
#   4. If rebase conflicts: close the PR with a comment, reopen the original issue
#
# Reads: /var/run/hive-metrics/actionable.json (PR list)
# Writes: /var/run/hive-metrics/conflict-sweep.json (sweep results)
# Rate limit: processes at most MAX_REBASES_PER_RUN PRs to avoid token/API burn

set -euo pipefail

# shellcheck source=hive-config.sh
source "$(dirname "$0")/hive-config.sh" 2>/dev/null || source /usr/local/bin/hive-config.sh 2>/dev/null || true

ACTIONABLE_FILE="/var/run/hive-metrics/actionable.json"
OUTPUT_FILE="/var/run/hive-metrics/conflict-sweep.json"
LOG="/var/log/kick-agents.log"
MAX_REBASES_PER_RUN=3
CONFLICT_AGE_MINUTES=30
WORKDIR="/tmp/conflict-sweeper-work"

log() { echo "[$(date -Is)] CONFLICT-SWEEP $*" >> "$LOG"; }

if [ ! -f "$ACTIONABLE_FILE" ]; then
  log "SKIP — actionable.json not found"
  exit 0
fi

log "START"

# Find CONFLICTING PRs from actionable list
conflicting=$(python3 -c "
import json, subprocess, sys, os

with open('$ACTIONABLE_FILE') as f:
    data = json.load(f)

ai_authors = {os.environ.get('PROJECT_AI_AUTHOR', ''), 'copilot-swe-agent[bot]', 'github-actions[bot]'} - {''}
prs = data.get('prs', {}).get('items', [])
results = []

for p in prs:
    repo = p['repo']
    num = p['number']
    age = p.get('age_minutes', 0)

    # Only sweep PRs older than threshold
    if age < $CONFLICT_AGE_MINUTES:
        continue

    # Check mergeable status
    try:
        info = subprocess.run(
            ['gh', 'pr', 'view', str(num), '--repo', repo, '--json', 'mergeable,author,headRefName,body'],
            capture_output=True, text=True, timeout=30
        )
        if info.returncode != 0:
            continue
        pr_data = json.loads(info.stdout)
        author = pr_data.get('author', {}).get('login', '')
        if author not in ai_authors:
            continue
        if pr_data.get('mergeable') != 'CONFLICTING':
            continue

        # Extract linked issue from PR body (Fixes #NNN pattern)
        body = pr_data.get('body', '')
        import re
        issue_match = re.search(r'Fixes\s+(?:\S+)?#(\d+)', body)
        issue_num = issue_match.group(1) if issue_match else ''

        results.append({
            'repo': repo,
            'number': num,
            'branch': pr_data.get('headRefName', ''),
            'linked_issue': issue_num,
            'age_minutes': age
        })
    except Exception:
        continue

# Sort by age descending (oldest first), limit to max
results.sort(key=lambda x: x['age_minutes'], reverse=True)
print(json.dumps(results[:$MAX_REBASES_PER_RUN]))
" 2>/dev/null)

if [ "$conflicting" = "[]" ] || [ -z "$conflicting" ]; then
  python3 -c "
import json
from datetime import datetime, timezone
print(json.dumps({
    'generated_at': datetime.now(timezone.utc).isoformat(),
    'rebased': [], 'closed': [], 'skipped': 0
}, indent=2))
" > "$OUTPUT_FILE"
  log "DONE — no conflicting PRs to sweep"
  exit 0
fi

# Process each conflicting PR
rebased=()
closed=()

rm -rf "$WORKDIR"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR"' EXIT

echo "$conflicting" | python3 -c "
import json, subprocess, sys, os, shutil

prs = json.load(sys.stdin)
workdir = '$WORKDIR'
rebased = []
closed = []

for pr in prs:
    repo = pr['repo']
    num = pr['number']
    branch = pr['branch']
    issue = pr.get('linked_issue', '')
    pr_dir = os.path.join(workdir, f'{repo.replace(\"/\", \"_\")}_{num}')

    print(f'  Processing #{num} ({repo}) branch={branch}...', file=sys.stderr)

    try:
        # Shallow clone
        clone = subprocess.run(
            ['git', 'clone', '--depth=50', f'https://github.com/{repo}.git', pr_dir],
            capture_output=True, text=True, timeout=60
        )
        if clone.returncode != 0:
            print(f'  #{num}: clone failed — skipping', file=sys.stderr)
            continue

        # Fetch the PR branch
        fetch = subprocess.run(
            ['git', 'fetch', 'origin', f'{branch}:{branch}'],
            capture_output=True, text=True, timeout=30,
            cwd=pr_dir
        )
        if fetch.returncode != 0:
            print(f'  #{num}: fetch branch failed — skipping', file=sys.stderr)
            continue

        # Checkout PR branch
        subprocess.run(['git', 'checkout', branch], capture_output=True, text=True, cwd=pr_dir)

        # Attempt rebase onto origin/main
        rebase = subprocess.run(
            ['git', 'rebase', 'origin/main'],
            capture_output=True, text=True, timeout=60,
            cwd=pr_dir
        )

        if rebase.returncode == 0:
            # Clean rebase — force-push
            push = subprocess.run(
                ['git', 'push', '--force-with-lease', 'origin', branch],
                capture_output=True, text=True, timeout=30,
                cwd=pr_dir
            )
            if push.returncode == 0:
                print(f'  #{num}: rebased successfully', file=sys.stderr)
                rebased.append({'repo': repo, 'number': num, 'action': 'rebased'})
            else:
                print(f'  #{num}: rebase ok but push failed — {push.stderr[:100]}', file=sys.stderr)
        else:
            # Rebase conflicts — abort, close PR, reopen issue
            subprocess.run(['git', 'rebase', '--abort'], capture_output=True, text=True, cwd=pr_dir)

            # Close PR with comment
            comment = f'Closing: rebase onto main has conflicts that cannot be auto-resolved. '
            if issue:
                comment += f'Reopening #{issue} for a fresh fix attempt.'
            subprocess.run(
                ['gh', 'pr', 'close', str(num), '--repo', repo, '--comment', comment],
                capture_output=True, text=True, timeout=30
            )

            # Reopen the linked issue if it was closed
            if issue:
                subprocess.run(
                    ['gh', 'issue', 'reopen', issue, '--repo', repo],
                    capture_output=True, text=True, timeout=15
                )

            print(f'  #{num}: conflicts — closed PR, reopened #{issue}', file=sys.stderr)
            closed.append({'repo': repo, 'number': num, 'action': 'closed_conflicting', 'issue_reopened': issue})

    except Exception as e:
        print(f'  #{num}: error — {str(e)[:100]}', file=sys.stderr)
    finally:
        shutil.rmtree(pr_dir, ignore_errors=True)

# Write results
import json as j
from datetime import datetime, timezone
result = {
    'generated_at': datetime.now(timezone.utc).isoformat(),
    'rebased': rebased,
    'closed': closed,
    'total_processed': len(rebased) + len(closed)
}
with open('$OUTPUT_FILE', 'w') as f:
    j.dump(result, f, indent=2)

print(f'Conflict sweep: {len(rebased)} rebased, {len(closed)} closed')
" 2>/dev/null

log "DONE — conflict sweep complete"
