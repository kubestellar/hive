#!/bin/bash
# hive-deploy.sh — pull latest hive repo and sync scripts to /usr/local/bin.
# Called by systemd timer every 60 seconds. Ensures the installed scripts
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

SYNCED=""
DASHBOARD_CHANGED=""

if [ "$BEFORE" != "$AFTER" ]; then
  CHANGED_FILES=$(git diff --name-only "$BEFORE" "$AFTER")
  SCRIPTS_CHANGED=$(echo "$CHANGED_FILES" | grep '^bin/' || true)
  for script in $SCRIPTS_CHANGED; do
    filename=$(basename "$script")
    src="$HIVE_REPO/$script"
    dst="$INSTALL_DIR/$filename"
    if [ -f "$src" ] && [ -f "$dst" ]; then
      sudo cp "$src" "$dst"
      sudo chmod +x "$dst"
      SYNCED="$SYNCED $filename"
    fi
  done
  DASHBOARD_CHANGED=$(echo "$CHANGED_FILES" | grep '^dashboard/' || true)
fi

# Drift check: even if HEAD unchanged, installed files may be stale
for src in "$HIVE_REPO"/bin/*.sh; do
  filename=$(basename "$src")
  dst="$INSTALL_DIR/$filename"
  [ -f "$dst" ] || continue
  if ! cmp -s "$src" "$dst"; then
    sudo cp "$src" "$dst"
    sudo chmod +x "$dst"
    SYNCED="$SYNCED $filename(drift)"
  fi
done

# gh-wrapper.sh is installed as /usr/local/bin/gh (ahead of /usr/bin/gh in PATH)
GH_WRAPPER="$HIVE_REPO/bin/gh-wrapper.sh"
GH_INSTALLED="$INSTALL_DIR/gh"
if [ -f "$GH_WRAPPER" ] && ! cmp -s "$GH_WRAPPER" "$GH_INSTALLED"; then
  sudo cp "$GH_WRAPPER" "$GH_INSTALLED"
  sudo chmod +x "$GH_INSTALLED"
  SYNCED="$SYNCED gh-wrapper→gh"
fi

# Restart dashboard if any dashboard/ files changed during pull
if [ -n "$DASHBOARD_CHANGED" ]; then
  sudo systemctl restart hive-dashboard.service 2>/dev/null && \
    SYNCED="$SYNCED dashboard(restart)" || \
    log "WARN: failed to restart hive-dashboard"
fi

# Dashboard drift check: restart if running process is older than dashboard files
DASH_RESTART_NEEDED=""
if systemctl is-active --quiet hive-dashboard.service 2>/dev/null; then
  DASH_PID=$(systemctl show hive-dashboard.service --property=MainPID --value 2>/dev/null)
  if [ -n "$DASH_PID" ] && [ "$DASH_PID" != "0" ]; then
    DASH_START=$(stat -c %Y "/proc/$DASH_PID" 2>/dev/null || echo 0)
    for df in "$HIVE_REPO"/dashboard/*.js "$HIVE_REPO"/dashboard/*.html; do
      [ -f "$df" ] || continue
      FILE_MTIME=$(stat -c %Y "$df" 2>/dev/null || echo 0)
      if [ "$FILE_MTIME" -gt "$DASH_START" ]; then
        DASH_RESTART_NEEDED="yes"
        break
      fi
    done
  fi
fi
if [ -n "$DASH_RESTART_NEEDED" ] && [ -z "$DASHBOARD_CHANGED" ]; then
  sudo systemctl restart hive-dashboard.service 2>/dev/null && \
    SYNCED="$SYNCED dashboard(drift-restart)" || \
    log "WARN: failed to restart hive-dashboard (drift)"
fi

if [ -n "$SYNCED" ]; then
  log "DEPLOY ${BEFORE:0:7}→${AFTER:0:7} — synced:$SYNCED"
fi
