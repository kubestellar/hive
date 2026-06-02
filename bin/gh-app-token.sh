#!/bin/bash
# gh-app-token.sh — Generate a GitHub App installation token for the hive.
# Refreshes automatically when called; tokens last 1 hour.
# Caches the token so repeated calls within the hour don't re-generate.
#
# Usage:
#   /usr/local/bin/gh-app-token.sh          # prints token to stdout
#   eval "$(/usr/local/bin/gh-app-token.sh --export)"  # exports GH_TOKEN

set -euo pipefail

APP_ID="${GH_APP_ID:?GH_APP_ID must be set (GitHub App → General → App ID)}"
INSTALLATION_ID="${GH_APP_INSTALLATION_ID:?GH_APP_INSTALLATION_ID must be set (org settings → Installed GitHub Apps → URL tail)}"
PRIVATE_KEY_FILE="${GH_APP_KEY_FILE:-/etc/hive/gh-app-key.pem}"
CACHE_FILE="/var/run/hive-metrics/gh-app-token.cache"
CACHE_MAX_AGE_SECONDS=3300  # refresh 5 min before expiry (tokens last 3600s)

# Check if cached token is still valid
if [ -f "$CACHE_FILE" ]; then
  cache_age=$(( $(date +%s) - $(stat -c %Y "$CACHE_FILE" 2>/dev/null || echo 0) ))
  if [ "$cache_age" -lt "$CACHE_MAX_AGE_SECONDS" ]; then
    TOKEN=$(cat "$CACHE_FILE")
    if [ "${1:-}" = "--export" ]; then
      echo "export GH_TOKEN=$TOKEN"
    else
      echo "$TOKEN"
    fi
    exit 0
  fi
fi

# Generate JWT
NOW=$(date +%s)
IAT=$((NOW - 60))
EXP=$((NOW + 540))  # 9 minutes (max 10)

HEADER=$(echo -n '{"alg":"RS256","typ":"JWT"}' | openssl base64 -e -A | tr '+/' '-_' | tr -d '=')
PAYLOAD=$(echo -n "{\"iat\":${IAT},\"exp\":${EXP},\"iss\":\"${APP_ID}\"}" | openssl base64 -e -A | tr '+/' '-_' | tr -d '=')
SIGNATURE=$(echo -n "${HEADER}.${PAYLOAD}" | openssl dgst -sha256 -sign "$PRIVATE_KEY_FILE" | openssl base64 -e -A | tr '+/' '-_' | tr -d '=')

JWT="${HEADER}.${PAYLOAD}.${SIGNATURE}"

# Exchange JWT for installation access token
RESPONSE=$(curl -s -X POST \
  -H "Authorization: Bearer ${JWT}" \
  -H "Accept: application/vnd.github+json" \
  "https://api.github.com/app/installations/${INSTALLATION_ID}/access_tokens")

TOKEN=$(echo "$RESPONSE" | jq -r '.token // empty')

if [ -z "$TOKEN" ]; then
  echo "ERROR: Failed to get installation token" >&2
  echo "$RESPONSE" >&2
  exit 1
fi

# Cache the token
mkdir -p "$(dirname "$CACHE_FILE")"
echo -n "$TOKEN" > "$CACHE_FILE"
chmod 600 "$CACHE_FILE"

if [ "${1:-}" = "--export" ]; then
  echo "export GH_TOKEN=$TOKEN"
else
  echo "$TOKEN"
fi
