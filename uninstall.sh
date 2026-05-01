#!/bin/bash
# uninstall.sh — remove hive scripts + systemd units.
# Two modes:
#
#   sudo ./uninstall.sh                     # remove single-instance + scripts + templates
#   sudo ./uninstall.sh --instance <name>   # remove just that named instance
#
# Env files under /etc/hive/ and heartbeat log files are left
# intact in both modes.
set -euo pipefail

BIN_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"

INSTANCE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --instance) INSTANCE="$2"; shift 2 ;;
    --instance=*) INSTANCE="${1#--instance=}"; shift ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "uninstall.sh must run as root (use sudo)" >&2
  exit 1
fi

load_env_if_present() {
  local env="$1"
  if [ -f "$env" ]; then
    # shellcheck disable=SC1090
    set -a; . "$env"; set +a
  fi
}

kill_session_if_present() {
  if [ -n "${AGENT_USER:-}" ] && id "$AGENT_USER" >/dev/null 2>&1; then
    sudo -u "$AGENT_USER" tmux kill-session -t "${AGENT_SESSION_NAME:-hive}" 2>/dev/null || true
  fi
}

if [ -n "$INSTANCE" ]; then
  # Remove just this named instance. Leave shared scripts + templated unit
  # files in place because other instances may use them.
  load_env_if_present "/etc/hive/${INSTANCE}.env"
  kill_session_if_present

  echo "==> stopping + disabling instance $INSTANCE"
  for unit in \
    "hive@${INSTANCE}.service" \
    "supervised-agent@${INSTANCE}.service"; do
    systemctl disable --now "$unit" 2>/dev/null || true
  done

  systemctl daemon-reload
  echo
  echo "Instance '$INSTANCE' removed."
  echo "Left intact: /etc/hive/${INSTANCE}.env, heartbeat log, shared scripts and templated units."
  exit 0
fi

# Full uninstall: stop everything, remove scripts and all unit files (both
# single-instance and templated).
load_env_if_present "/etc/hive/agent.env"
kill_session_if_present

echo "==> stopping + disabling single-instance units"
for unit in \
  hive.service \
  supervised-agent@scanner.service \
  supervised-agent@reviewer.service \
  supervised-agent@architect.service \
  supervised-agent@outreach.service \
  supervised-agent@supervisor.service; do
  systemctl disable --now "$unit" 2>/dev/null || true
done

echo "==> removing unit files (single + templated + legacy)"
for unit in \
  hive.service \
  "hive@.service" \
  "supervised-agent@.service" \
  agent-healthcheck.service \
  agent-healthcheck.timer \
  agent-renew.service \
  agent-renew.timer; do
  rm -f "$SYSTEMD_DIR/$unit"
done

echo "==> removing scripts"
rm -f "$BIN_DIR/supervisor.sh" "$BIN_DIR/agent-healthcheck.sh" \
      "$BIN_DIR/agent-launch.sh" "$BIN_DIR/kick-agents.sh" \
      "$BIN_DIR/kick-governor.sh" "$BIN_DIR/notify.sh" \
      "$BIN_DIR/supervisor-kick.sh" "$BIN_DIR/hive-config.sh" \
      "$BIN_DIR/gh-wrapper.sh"
# Clean up legacy names from prior installs.
rm -f "$BIN_DIR/agent-supervisor.sh" "$BIN_DIR/agent-pause.sh"

echo "==> systemctl daemon-reload"
systemctl daemon-reload

echo
echo "Removed scripts + all unit files. Left intact:"
echo "  /etc/hive/*.env"
echo "  Heartbeat log files"
echo "  /tmp/hive-healthcheck/ (state dir, wiped on reboot)"
