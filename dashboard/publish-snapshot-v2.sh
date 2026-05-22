#!/usr/bin/env bash
# Multi-instance snapshot publisher for Hive v2.
#
# Publishes read-only dashboard snapshots to kubestellar.io/live/hive/<instance>/
# Each instance gets light + classic modes at:
#   public/live/hive/<instance>/index.html        (default = light)
#   public/live/hive/<instance>/light/index.html
#   public/live/hive/<instance>/classic/index.html
#
# Usage:
#   ./publish-snapshot-v2.sh <instance> <dashboard_url>
#
# Examples:
#   ./publish-snapshot-v2.sh bluefin http://192.168.4.85:3001
#   ./publish-snapshot-v2.sh console http://192.168.4.56:3001
#
# The default (no instance) path at /live/hive/ is a separate deployment
# managed by the original publish-snapshot.sh.
#
# Env vars:
#   DOCS_REPO_DIR  — local clone of kubestellar/docs (default: /tmp/kubestellar-docs-snapshot)

set -euo pipefail

export PATH="/usr/local/bin:$PATH"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ $# -lt 2 ]; then
  echo "Usage: $0 <instance-name> <dashboard-url>"
  echo "  e.g.: $0 bluefin http://192.168.4.85:3001"
  exit 1
fi

INSTANCE="$1"
DASHBOARD_URL="$2"
DOCS_REPO="${DOCS_REPO_DIR:-/tmp/kubestellar-docs-snapshot}"
DOCS_REPO_SLUG="kubestellar/docs"
PUBLISH_PATH="public/live/hive/${INSTANCE}"
BASE_PATH="/live/hive/${INSTANCE}"

GH_APP_TOKEN_FILE="/var/run/hive-metrics/gh-app-token.cache"
if [ -f "$GH_APP_TOKEN_FILE" ]; then
  GH_APP_TOKEN=$(cat "$GH_APP_TOKEN_FILE")
  DOCS_REMOTE="https://x-access-token:${GH_APP_TOKEN}@github.com/${DOCS_REPO_SLUG}.git"
  export GH_TOKEN="$GH_APP_TOKEN"
else
  echo "ERROR: no GitHub App token at $GH_APP_TOKEN_FILE"
  exit 1
fi

echo "[${INSTANCE}] Snapshot from ${DASHBOARD_URL} → ${BASE_PATH}"

# Verify dashboard is reachable
if ! curl -sf --max-time 10 "${DASHBOARD_URL}/api/status" >/dev/null 2>&1; then
  echo "ERROR: dashboard at ${DASHBOARD_URL} is not reachable"
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

mkdir -p "${PUBLISH_PATH}/light" "${PUBLISH_PATH}/classic"

node "${SCRIPT_DIR}/build-snapshot.mjs" \
  --mode light --base-path "$BASE_PATH" \
  "$DASHBOARD_URL" "${PUBLISH_PATH}/light/index.html"

node "${SCRIPT_DIR}/build-snapshot.mjs" \
  --mode classic --base-path "$BASE_PATH" \
  "$DASHBOARD_URL" "${PUBLISH_PATH}/classic/index.html"

cp "${PUBLISH_PATH}/light/index.html" "${PUBLISH_PATH}/index.html"

# Stage first so both new and modified files are detected
git add "${PUBLISH_PATH}/"
if git diff --cached --quiet -- "${PUBLISH_PATH}/"; then
  echo "[${INSTANCE}] No changes — skipping."
  git reset HEAD -- "${PUBLISH_PATH}/" >/dev/null 2>&1
  exit 0
fi

TIMESTAMP=$(date -u '+%Y-%m-%d %H:%M UTC')
SNAPSHOT_BRANCH="chore/hive-snapshot-${INSTANCE}-$(date -u '+%Y%m%d-%H%M%S')"

git checkout -b "$SNAPSHOT_BRANCH"
git add "${PUBLISH_PATH}/"
git commit -s -m "chore: update hive snapshot (${INSTANCE}) ${TIMESTAMP}"
git push origin "$SNAPSHOT_BRANCH"

PR_URL=$(gh pr create \
  --repo "$DOCS_REPO_SLUG" \
  --title "chore: hive snapshot (${INSTANCE}) ${TIMESTAMP}" \
  --body "Automated snapshot update for **${INSTANCE}** from ${DASHBOARD_URL}." \
  --head "$SNAPSHOT_BRANCH" \
  --base main 2>&1)

PR_NUM=$(echo "$PR_URL" | grep -o '[0-9]*$')
echo "[${INSTANCE}] Created PR #${PR_NUM}: ${PR_URL}"

NETLIFY_CHECK="netlify/kubestellar-docs/deploy-preview"
NETLIFY_TIMEOUT_SECONDS=300
NETLIFY_POLL_INTERVAL=15
elapsed=0
netlify_status="pending"

echo "[${INSTANCE}] Waiting for Netlify deploy-preview..."
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
done

if [ "$netlify_status" = "pass" ]; then
  echo "[${INSTANCE}] Netlify passed."
  if gh pr merge "$PR_NUM" --repo "$DOCS_REPO_SLUG" --admin --squash --delete-branch 2>/dev/null; then
    echo "[${INSTANCE}] Snapshot published via PR #${PR_NUM}."
  else
    gh pr merge "$PR_NUM" --repo "$DOCS_REPO_SLUG" --squash --auto --delete-branch
    echo "[${INSTANCE}] Auto-merge enabled on PR #${PR_NUM}."
  fi
elif [ "$netlify_status" = "fail" ]; then
  echo "[${INSTANCE}] ERROR: Netlify FAILED for PR #${PR_NUM}. NOT merging."
  if [ -n "${NTFY_TOPIC:-}" ]; then
    curl -s -d "Netlify failed for ${INSTANCE} snapshot PR #${PR_NUM}" \
      -H "Title: Hive snapshot blocked (${INSTANCE})" -H "Priority: high" \
      "${NTFY_SERVER:-https://ntfy.sh}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
  fi
  exit 1
else
  echo "[${INSTANCE}] WARNING: Netlify timed out for PR #${PR_NUM}. NOT merging."
  if [ -n "${NTFY_TOPIC:-}" ]; then
    curl -s -d "Netlify timed out for ${INSTANCE} snapshot PR #${PR_NUM}" \
      -H "Title: Hive snapshot timeout (${INSTANCE})" -H "Priority: default" \
      "${NTFY_SERVER:-https://ntfy.sh}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
  fi
  exit 1
fi

git checkout main
git reset --hard origin/main
