#!/bin/bash
# uninstall.sh — remove supervised-agent scripts + systemd units.
# Two modes:
#
#   sudo ./uninstall.sh                     # remove single-instance + scripts + templates
#   sudo ./uninstall.sh --instance <name>   # remove just that named instance
#
# Env files under /etc/supervised-agent/ and heartbeat log files are left
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
    sudo -u "$AGENT_USER" tmux kill-session -t "${AGENT_SESSION_NAME:-supervised-agent}" 2>/dev/null || true
  fi
}

if [ -n "$INSTANCE" ]; then
  # Remove just this named instance. Leave shared scripts + templated unit
  # files in place because other instances may use them.
  load_env_if_present "/etc/supervised-agent/${INSTANCE}.env"
  kill_session_if_present

  echo "==> stopping + disabling instance $INSTANCE"
  for unit in \
    "supervised-agent-healthcheck@${INSTANCE}.timer" \
    "supervised-agent-renew@${INSTANCE}.timer" \
    "supervised-agent@${INSTANCE}.service"; do
    systemctl disable --now "$unit" 2>/dev/null || true
  done

  systemctl daemon-reload
  echo
  echo "Instance '$INSTANCE' removed."
  echo "Left intact: /etc/supervised-agent/${INSTANCE}.env, heartbeat log, shared scripts and templated units."
  exit 0
fi

# Full uninstall: stop everything, remove scripts and all unit files (both
# single-instance and templated).
load_env_if_present "/etc/supervised-agent/agent.env"
kill_session_if_present

echo "==> stopping + disabling single-instance units"
for unit in \
  supervised-agent-healthcheck.timer \
  supervised-agent-renew.timer \
  supervised-agent.service; do
  systemctl disable --now "$unit" 2>/dev/null || true
done

echo "==> removing unit files (single + templated)"
for unit in \
  supervised-agent.service \
  supervised-agent-renew.service \
  supervised-agent-renew.timer \
  supervised-agent-healthcheck.service \
  supervised-agent-healthcheck.timer \
  "supervised-agent@.service" \
  "supervised-agent-renew@.service" \
  "supervised-agent-renew@.timer" \
  "supervised-agent-healthcheck@.service" \
  "supervised-agent-healthcheck@.timer"; do
  rm -f "$SYSTEMD_DIR/$unit"
done

echo "==> removing scripts"
rm -f "$BIN_DIR/agent-launch.sh" "$BIN_DIR/agent-supervisor.sh" "$BIN_DIR/agent-healthcheck.sh"

echo "==> systemctl daemon-reload"
systemctl daemon-reload

echo
echo "Removed scripts + all unit files. Left intact:"
echo "  /etc/supervised-agent/*.env"
echo "  Heartbeat log files"
echo "  /tmp/supervised-agent-healthcheck/ (state dir, wiped on reboot)"
