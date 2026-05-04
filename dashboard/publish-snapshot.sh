#!/usr/bin/env bash
# Build a dashboard snapshot and push it to the docs repo.
# Designed to run as a cron job on the hive server.
#
# Uses a branch + PR + admin-merge workflow because kubestellar/docs
# has branch protection on main requiring status checks.
#
# Usage: ./publish-snapshot.sh
# Env vars:
#   HIVE_DASHBOARD_URL  — dashboard URL (default: http://localhost:3001)
#   DOCS_REPO_DIR       — local clone of kubestellar/docs (default: /tmp/kubestellar-docs-snapshot)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DASHBOARD_URL="${HIVE_DASHBOARD_URL:-http://localhost:3001}"
DOCS_REPO="${DOCS_REPO_DIR:-/tmp/kubestellar-docs-snapshot}"
OUTPUT_DIR="${DOCS_REPO}/public/live/hive"
BRANCH="main"
SNAPSHOT_BRANCH="chore/hive-snapshot"
DOCS_REPO_SLUG="kubestellar/docs"
GH_CLI="/usr/bin/gh"

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
  git clone --depth 1 --single-branch -b "$BRANCH" "$DOCS_REMOTE" "$DOCS_REPO"
fi

cd "$DOCS_REPO"
git remote set-url origin "$DOCS_REMOTE"
git fetch origin "$BRANCH"
git checkout "$BRANCH" 2>/dev/null || git checkout -b "$BRANCH" "origin/$BRANCH"
git reset --hard "origin/$BRANCH"

# Build snapshot
mkdir -p "$OUTPUT_DIR"
node "${SCRIPT_DIR}/build-snapshot.mjs" "$DASHBOARD_URL" "${OUTPUT_DIR}/index.html"

# Check if anything changed
if git diff --quiet -- public/live/hive/; then
  echo "No changes to snapshot — skipping commit."
  exit 0
fi

TIMESTAMP=$(date -u '+%Y-%m-%d %H:%M UTC')

# Create snapshot branch from current main
git checkout -B "$SNAPSHOT_BRANCH"
git add public/live/hive/index.html
git commit -s -m "chore: update hive dashboard snapshot $TIMESTAMP"
git push origin "$SNAPSHOT_BRANCH" --force

# Close any existing snapshot PR before creating a new one
EXISTING_PR=$($GH_CLI pr list --repo "$DOCS_REPO_SLUG" --head "$SNAPSHOT_BRANCH" --json number --jq '.[0].number' 2>/dev/null || true)
if [ -n "$EXISTING_PR" ]; then
  $GH_CLI pr close "$EXISTING_PR" --repo "$DOCS_REPO_SLUG" 2>/dev/null || true
fi

# Create PR and immediately admin-merge
PR_URL=$($GH_CLI pr create --repo "$DOCS_REPO_SLUG" \
  --head "$SNAPSHOT_BRANCH" --base "$BRANCH" \
  --title "chore: hive dashboard snapshot $TIMESTAMP" \
  --body "Automated snapshot update from hive dashboard." 2>&1)

PR_NUM=$(echo "$PR_URL" | grep -oE '[0-9]+$')

if [ -n "$PR_NUM" ]; then
  $GH_CLI pr merge "$PR_NUM" --repo "$DOCS_REPO_SLUG" --admin --squash --delete-branch 2>&1 && \
    echo "Snapshot published via PR #$PR_NUM." || \
    echo "WARN: PR #$PR_NUM created but merge failed — manual merge needed."
else
  echo "ERROR: could not create PR. Output: $PR_URL"
  exit 1
fi

# Return to main for next run
git checkout "$BRANCH" 2>/dev/null || true
