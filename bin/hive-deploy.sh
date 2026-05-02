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
DISCORD_CHANGED=""

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
  DISCORD_CHANGED=$(echo "$CHANGED_FILES" | grep '^discord/' || true)
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

# hive.sh is installed as /usr/local/bin/hive (no .sh extension)
HIVE_CLI="$HIVE_REPO/bin/hive.sh"
HIVE_INSTALLED="$INSTALL_DIR/hive"
if [ -f "$HIVE_CLI" ] && ! cmp -s "$HIVE_CLI" "$HIVE_INSTALLED"; then
  sudo cp "$HIVE_CLI" "$HIVE_INSTALLED"
  sudo chmod +x "$HIVE_INSTALLED"
  SYNCED="$SYNCED hive.sh→hive"
fi

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

# Install Discord bot dependencies if package.json changed or node_modules missing
if [ -n "$DISCORD_CHANGED" ] || [ ! -d "$HIVE_REPO/discord/node_modules" ]; then
  (cd "$HIVE_REPO/discord" && npm install --production 2>/dev/null) && \
    SYNCED="$SYNCED discord(npm-install)" || \
    log "WARN: failed to npm install in discord/"
fi

# Restart Discord bot if any discord/ files changed during pull
if [ -n "$DISCORD_CHANGED" ]; then
  sudo systemctl restart hive-discord.service 2>/dev/null && \
    SYNCED="$SYNCED discord(restart)" || \
    log "WARN: failed to restart hive-discord"
fi

# Discord bot drift check: restart if running process is older than discord files
DISCORD_RESTART_NEEDED=""
if systemctl is-active --quiet hive-discord.service 2>/dev/null; then
  DISCORD_PID=$(systemctl show hive-discord.service --property=MainPID --value 2>/dev/null)
  if [ -n "$DISCORD_PID" ] && [ "$DISCORD_PID" != "0" ]; then
    DISCORD_START=$(stat -c %Y "/proc/$DISCORD_PID" 2>/dev/null || echo 0)
    for df in "$HIVE_REPO"/discord/*.js "$HIVE_REPO"/discord/lib/*.js; do
      [ -f "$df" ] || continue
      FILE_MTIME=$(stat -c %Y "$df" 2>/dev/null || echo 0)
      if [ "$FILE_MTIME" -gt "$DISCORD_START" ]; then
        DISCORD_RESTART_NEEDED="yes"
        break
      fi
    done
  fi
fi
if [ -n "$DISCORD_RESTART_NEEDED" ] && [ -z "$DISCORD_CHANGED" ]; then
  sudo systemctl restart hive-discord.service 2>/dev/null && \
    SYNCED="$SYNCED discord(drift-restart)" || \
    log "WARN: failed to restart hive-discord (drift)"
fi

# Sync hive-project.yaml to /etc/hive if changed
HIVE_PROJECT="${HIVE_PROJECT_CONFIG_SRC:-$HIVE_REPO/examples/kubestellar/hive-project.yaml}"
HIVE_PROJECT_INSTALLED="/etc/hive/hive-project.yaml"
if [ -f "$HIVE_PROJECT" ] && ! cmp -s "$HIVE_PROJECT" "$HIVE_PROJECT_INSTALLED" 2>/dev/null; then
  sudo cp "$HIVE_PROJECT" "$HIVE_PROJECT_INSTALLED" && \
    SYNCED="$SYNCED hive-project.yaml" || \
    log "WARN: failed to sync hive-project.yaml"
fi

# Sync systemd units if changed
for unit in "$HIVE_REPO"/systemd/*.service "$HIVE_REPO"/systemd/*.timer; do
  [ -f "$unit" ] || continue
  unitname=$(basename "$unit")
  dst="/etc/systemd/system/$unitname"
  if [ -f "$dst" ] && cmp -s "$unit" "$dst"; then
    continue
  fi
  sudo cp "$unit" "$dst" && SYNCED="$SYNCED $unitname" || true
done
if echo "$SYNCED" | grep -q '\.service\|\.timer'; then
  sudo systemctl daemon-reload 2>/dev/null || true
fi

# Ensure snapshot timer is enabled
if [ -f /etc/systemd/system/hive-snapshot.timer ] && ! systemctl is-enabled --quiet hive-snapshot.timer 2>/dev/null; then
  sudo systemctl enable --now hive-snapshot.timer 2>/dev/null && \
    SYNCED="$SYNCED hive-snapshot.timer(enabled)" || true
fi

# Ensure per-agent watchdog services are enabled and running.
# Each agent gets its own hive@<name>.service backed by supervisor.sh,
# which monitors the tmux session and restarts if it dies.
# Migrate from monolithic hive.service to per-agent hive@<name>.service.
# The old hive.service only watchdogged the supervisor; per-agent units
# give each agent its own watchdog with Restart=always.
# Don't stop the old service mid-run (its tmux sessions are independent),
# just disable it so it won't start on next boot.
if systemctl is-enabled --quiet hive.service 2>/dev/null; then
  sudo systemctl disable hive.service 2>/dev/null || true
  SYNCED="$SYNCED hive.service(disabled)"
fi

_DEPLOY_SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
for _cf in "${_DEPLOY_SCRIPT_DIR}/hive-config.sh" /usr/local/bin/hive-config.sh; do
  if [[ -f "$_cf" ]]; then source "$_cf"; break; fi
done
HIVE_AGENTS="${AGENTS_ENABLED:-supervisor scanner reviewer architect outreach}"
for agent in $HIVE_AGENTS; do
  unit="hive@${agent}.service"
  envfile="/etc/hive/${agent}.env"
  [ -f "$envfile" ] || continue
  if ! systemctl is-enabled --quiet "$unit" 2>/dev/null; then
    sudo systemctl enable "$unit" 2>/dev/null && \
      SYNCED="$SYNCED ${unit}(enabled)" || true
  fi
  if ! systemctl is-active --quiet "$unit" 2>/dev/null; then
    sudo systemctl start "$unit" 2>/dev/null && \
      SYNCED="$SYNCED ${unit}(started)" || \
      log "WARN: failed to start $unit"
  fi
done

if [ -n "$SYNCED" ]; then
  log "DEPLOY ${BEFORE:0:7}→${AFTER:0:7} — synced:$SYNCED"
fi
