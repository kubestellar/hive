#!/bin/bash
# git-credential-hive.sh — Git credential helper that uses the GitHub App token.
# Reads from the cached token file, refreshing if stale (>55 min old).
# Install: git config --global credential.https://github.com.helper /usr/local/bin/git-credential-hive.sh

set -euo pipefail

# ── ACMM enforcement: block git push for advisory-only agents ──
AGENT="${HIVE_AGENT:-}"
ACMM="${HIVE_ACMM_LEVEL:-0}"

if [ -n "$AGENT" ] && [ "$ACMM" -gt 0 ]; then
  # L1/L2: no agent can push
  if [ "$ACMM" -lt 3 ]; then
    echo "⛔ git push blocked: ACMM L${ACMM} agents are advisory-only" >&2
    exit 1
  fi
  # L3: only quality can push
  if [ "$ACMM" -eq 3 ] && [ "$AGENT" != "quality" ]; then
    echo "⛔ git push blocked: only quality agent can push at ACMM L3" >&2
    exit 1
  fi
fi

TOKEN_FILE="/var/run/hive-metrics/gh-app-token.cache"
CACHE_MAX_AGE_SECONDS=3300

refresh_token() {
  if [ -x /usr/local/bin/gh-app-token.sh ]; then
    /usr/local/bin/gh-app-token.sh >/dev/null 2>&1 || true
  fi
}

if [ ! -f "$TOKEN_FILE" ]; then
  refresh_token
fi

if [ -f "$TOKEN_FILE" ]; then
  cache_age=$(( $(date +%s) - $(stat -c %Y "$TOKEN_FILE" 2>/dev/null || echo 0) ))
  if [ "$cache_age" -gt "$CACHE_MAX_AGE_SECONDS" ]; then
    refresh_token
  fi
fi

TOKEN=$(cat "$TOKEN_FILE" 2>/dev/null || true)
if [ -z "$TOKEN" ]; then
  exit 1
fi

case "${1:-}" in
  get)
    echo "protocol=https"
    echo "host=github.com"
    echo "username=x-access-token"
    echo "password=$TOKEN"
    ;;
esac
