#!/usr/bin/env bash
# Build dashboard snapshots (light + classic) and push to the docs repo.
# Creates a PR and merges with --admin to satisfy branch protection.
#
# Produces:
#   public/live/hive/index.html        — default (light mode)
#   public/live/hive/light/index.html   — light mode
#   public/live/hive/classic/index.html — classic mode
#
# Usage: ./publish-snapshot.sh
# Env vars:
#   HIVE_DASHBOARD_URL  — dashboard URL (default: http://localhost:3001)
#   DOCS_REPO_DIR       — local clone of kubestellar/docs (default: /tmp/kubestellar-docs-snapshot)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DASHBOARD_URL="${HIVE_DASHBOARD_URL:-http://localhost:3001}"
DOCS_REPO="${DOCS_REPO_DIR:-/tmp/kubestellar-docs-snapshot}"
DOCS_REPO_SLUG="kubestellar/docs"

GH_APP_TOKEN_FILE="/var/run/hive-metrics/gh-app-token.cache"
if [ -f "$GH_APP_TOKEN_FILE" ]; then
  GH_APP_TOKEN=$(cat "$GH_APP_TOKEN_FILE")
  DOCS_REMOTE="https://x-access-token:${GH_APP_TOKEN}@github.com/${DOCS_REPO_SLUG}.git"
  export GH_TOKEN="$GH_APP_TOKEN"
else
  echo "ERROR: no GitHub App token at $GH_APP_TOKEN_FILE"
  exit 1
fi

# Ensure docs repo clone exists
if [ ! -d "$DOCS_REPO/.git" ]; then
  git clone --depth 1 --single-branch -b main "$DOCS_REMOTE" "$DOCS_REPO"
fi

cd "$DOCS_REPO"
git remote set-url origin "$DOCS_REMOTE"
git fetch origin main
git checkout main 2>/dev/null || git checkout -b main origin/main
git reset --hard origin/main

# Close any still-open snapshot PRs to prevent merge conflicts
echo "Closing stale snapshot PRs..."
open_prs=$(gh pr list --repo "$DOCS_REPO_SLUG" --author "app/kubestellar-hive" --search "chore: hive dashboard snapshot" --state open --json number --jq ".[].number" 2>/dev/null || true)
for pr in $open_prs; do
  echo "  Closing stale PR #${pr}"
  gh pr close "$pr" --repo "$DOCS_REPO_SLUG" --delete-branch 2>/dev/null || true
done

# Build all three snapshots from the same API data
mkdir -p public/live/hive/light public/live/hive/classic

node "${SCRIPT_DIR}/build-snapshot.mjs" --mode light "$DASHBOARD_URL" public/live/hive/light/index.html
node "${SCRIPT_DIR}/build-snapshot.mjs" --mode classic "$DASHBOARD_URL" public/live/hive/classic/index.html
cp public/live/hive/light/index.html public/live/hive/index.html

# Check if anything changed
if git diff --quiet -- public/live/hive/; then
  echo "No changes to snapshot — skipping."
  exit 0
fi

TIMESTAMP=$(date -u '+%Y-%m-%d %H:%M UTC')

# Re-fetch and rebase onto latest main so the PR won't conflict
git stash --include-untracked
git fetch origin main
git reset --hard origin/main
git stash pop

SNAPSHOT_BRANCH="chore/hive-snapshot-$(date -u '+%Y%m%d-%H%M%S')"

git checkout -b "$SNAPSHOT_BRANCH"
git add public/live/hive/
git commit -s -m "chore: update hive dashboard snapshot $TIMESTAMP"
git push origin "$SNAPSHOT_BRANCH"

PR_URL=$(gh pr create \
  --repo "$DOCS_REPO_SLUG" \
  --title "chore: hive dashboard snapshot $TIMESTAMP" \
  --body "Automated snapshot update from hive server." \
  --head "$SNAPSHOT_BRANCH" \
  --base main 2>&1)

PR_NUM=$(echo "$PR_URL" | grep -o '[0-9]*$')
echo "Created PR #${PR_NUM}: ${PR_URL}"

# Wait for Netlify deploy-preview before merging
NETLIFY_CHECK="netlify/kubestellar-docs/deploy-preview"
NETLIFY_TIMEOUT_SECONDS=300
NETLIFY_POLL_INTERVAL=15
elapsed=0
netlify_status="pending"

echo "Waiting for Netlify deploy-preview (timeout: ${NETLIFY_TIMEOUT_SECONDS}s)..."
while [ "$elapsed" -lt "$NETLIFY_TIMEOUT_SECONDS" ]; do
  checks_output=$(gh pr checks "$PR_NUM" --repo "$DOCS_REPO_SLUG" 2>/dev/null || true)
  netlify_line=$(echo "$checks_output" | grep -i "$NETLIFY_CHECK" || true)

  if [ -n "$netlify_line" ]; then
    if echo "$netlify_line" | grep -qi "pass"; then
      netlify_status="pass"
      break
    elif echo "$netlify_line" | grep -qi "fail"; then
      netlify_status="fail"
      break
    fi
  fi

  sleep "$NETLIFY_POLL_INTERVAL"
  elapsed=$((elapsed + NETLIFY_POLL_INTERVAL))
  echo "  ...waiting (${elapsed}s/${NETLIFY_TIMEOUT_SECONDS}s)"
done

if [ "$netlify_status" = "pass" ]; then
  echo "Netlify deploy-preview passed."
  if gh pr merge "$PR_NUM" --repo "$DOCS_REPO_SLUG" --admin --squash --delete-branch 2>/dev/null; then
    echo "Snapshot published via PR #${PR_NUM} (admin merge)."
  else
    echo "Admin merge unavailable — enabling auto-merge on PR #${PR_NUM}."
    gh pr merge "$PR_NUM" --repo "$DOCS_REPO_SLUG" --squash --auto --delete-branch
    echo "Auto-merge enabled on PR #${PR_NUM} — will merge when checks pass."
  fi
elif [ "$netlify_status" = "fail" ]; then
  echo "ERROR: Netlify deploy-preview FAILED for PR #${PR_NUM}. NOT merging."
  echo "Check: https://github.com/${DOCS_REPO_SLUG}/pull/${PR_NUM}"
  # Send ntfy alert if available
  if [ -n "${NTFY_TOPIC:-}" ]; then
    curl -s -d "Netlify deploy-preview failed for snapshot PR #${PR_NUM} — not merged" \
      -H "Title: Hive snapshot blocked" -H "Priority: high" \
      "${NTFY_SERVER:-https://ntfy.sh}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
  fi
  exit 1
else
  echo "WARNING: Netlify deploy-preview timed out after ${NETLIFY_TIMEOUT_SECONDS}s for PR #${PR_NUM}. NOT merging."
  echo "Check: https://github.com/${DOCS_REPO_SLUG}/pull/${PR_NUM}"
  if [ -n "${NTFY_TOPIC:-}" ]; then
    curl -s -d "Netlify deploy-preview timed out for snapshot PR #${PR_NUM} — not merged" \
      -H "Title: Hive snapshot timeout" -H "Priority: default" \
      "${NTFY_SERVER:-https://ntfy.sh}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
  fi
  exit 1
fi

# Reset back to main for next run
git checkout main
git reset --hard origin/main
