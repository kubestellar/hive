#!/bin/bash
# hive-deploy.sh — pull latest hive repo and sync scripts to /usr/local/bin.
# Called by systemd timer every 5 minutes. Ensures the installed scripts
# always match the repo without manual SCP or copy steps.

set -euo pipefail

HIVE_REPO="${HIVE_REPO_DIR:-/tmp/hive}"
INSTALL_DIR="/usr/local/bin"
LOG="/var/log/hive-deploy.log"
TIMESTAMP="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

log() { echo "[$TIMESTAMP] $*" >> "$LOG" 2>/dev/null || true; }

if [ ! -d "$HIVE_REPO/.git" ]; then
  log "ERROR: $HIVE_REPO is not a git repo"
  exit 1
fi

cd "$HIVE_REPO"

BEFORE=$(git rev-parse HEAD)
git pull --rebase origin main --quiet 2>/dev/null || {
  log "WARN: git pull failed, skipping deploy"
  exit 0
}
AFTER=$(git rev-parse HEAD)

if [ "$BEFORE" = "$AFTER" ]; then
  exit 0
fi

CHANGED_FILES=$(git diff --name-only "$BEFORE" "$AFTER")
SCRIPTS_CHANGED=$(echo "$CHANGED_FILES" | grep '^bin/' || true)

if [ -z "$SCRIPTS_CHANGED" ]; then
  log "PULL $BEFORE→$AFTER — no bin/ changes, skipping sync"
  exit 0
fi

SYNCED=""
for script in $SCRIPTS_CHANGED; do
  filename=$(basename "$script")
  src="$HIVE_REPO/$script"
  dst="$INSTALL_DIR/$filename"
  if [ -f "$src" ] && [ -f "$dst" ]; then
    cp "$src" "$dst"
    chmod +x "$dst"
    SYNCED="$SYNCED $filename"
  fi
done

if [ -n "$SYNCED" ]; then
  log "DEPLOY $BEFORE→$AFTER — synced:$SYNCED"
fi
