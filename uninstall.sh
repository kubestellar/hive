#!/bin/bash
# uninstall.sh — remove supervised-agent scripts + systemd units.
# Leaves /etc/supervised-agent/agent.env and any logs intact.
# Run as root:  sudo ./uninstall.sh
set -euo pipefail

BIN_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"
ENV_FILE="/etc/supervised-agent/agent.env"

if [ "$(id -u)" -ne 0 ]; then
  echo "uninstall.sh must run as root (use sudo)" >&2
  exit 1
fi

# Best-effort kill of any running session.
if [ -f "$ENV_FILE" ]; then
  # shellcheck disable=SC1090
  set -a; . "$ENV_FILE"; set +a
  if [ -n "${AGENT_USER:-}" ] && id "$AGENT_USER" >/dev/null 2>&1; then
    sudo -u "$AGENT_USER" tmux kill-session -t "${AGENT_SESSION_NAME:-supervised-agent}" 2>/dev/null || true
  fi
fi

echo "==> stopping + disabling units"
for unit in \
  supervised-agent-healthcheck.timer \
  supervised-agent-renew.timer \
  supervised-agent.service; do
  systemctl disable --now "$unit" 2>/dev/null || true
done

echo "==> removing unit files"
for unit in \
  supervised-agent.service \
  supervised-agent-renew.service \
  supervised-agent-renew.timer \
  supervised-agent-healthcheck.service \
  supervised-agent-healthcheck.timer; do
  rm -f "$SYSTEMD_DIR/$unit"
done

echo "==> removing scripts"
rm -f "$BIN_DIR/agent-launch.sh" "$BIN_DIR/agent-supervisor.sh" "$BIN_DIR/agent-healthcheck.sh"

echo "==> systemctl daemon-reload"
systemctl daemon-reload

echo
echo "Removed scripts + units. Left intact:"
echo "  $ENV_FILE"
echo "  Log file and its directory"
echo "  /tmp/supervised-agent-healthcheck/ (state dir, wiped on reboot)"
