#!/bin/bash
# install.sh — install supervised-agent scripts + systemd units.
# Run as root:  sudo ./install.sh
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="/etc/supervised-agent/agent.env"
BIN_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"

if [ "$(id -u)" -ne 0 ]; then
  echo "install.sh must run as root (use sudo)" >&2
  exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing $ENV_FILE"
  echo "Copy the template first:"
  echo "  sudo mkdir -p $(dirname "$ENV_FILE")"
  echo "  sudo cp $REPO_DIR/config/agent.env.example $ENV_FILE"
  echo "  sudo \$EDITOR $ENV_FILE"
  exit 1
fi

# Load env so we can validate + substitute AGENT_USER.
# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a

for var in AGENT_USER AGENT_WORKDIR AGENT_LAUNCH_CMD AGENT_LOOP_PROMPT AGENT_LOG_FILE; do
  if [ -z "${!var:-}" ]; then
    echo "Required env var $var is empty in $ENV_FILE" >&2
    exit 1
  fi
done

if ! id "$AGENT_USER" >/dev/null 2>&1; then
  echo "AGENT_USER '$AGENT_USER' does not exist on this system" >&2
  exit 1
fi

echo "==> installing scripts to $BIN_DIR"
install -m 0755 "$REPO_DIR/bin/agent-launch.sh"       "$BIN_DIR/agent-launch.sh"
install -m 0755 "$REPO_DIR/bin/agent-supervisor.sh"   "$BIN_DIR/agent-supervisor.sh"
install -m 0755 "$REPO_DIR/bin/agent-healthcheck.sh"  "$BIN_DIR/agent-healthcheck.sh"

echo "==> installing systemd units to $SYSTEMD_DIR (substituting User=$AGENT_USER)"
for unit in \
  supervised-agent.service \
  supervised-agent-renew.service \
  supervised-agent-renew.timer \
  supervised-agent-healthcheck.service \
  supervised-agent-healthcheck.timer; do
  sed "s/__AGENT_USER__/$AGENT_USER/g" "$REPO_DIR/systemd/$unit" \
    > "$SYSTEMD_DIR/$unit"
  chmod 0644 "$SYSTEMD_DIR/$unit"
done

echo "==> creating log dir for $AGENT_USER"
LOG_DIR="$(dirname "$AGENT_LOG_FILE")"
install -d -o "$AGENT_USER" -g "$AGENT_USER" -m 0755 "$LOG_DIR"

echo "==> systemctl daemon-reload"
systemctl daemon-reload

echo "==> enabling + starting units"
systemctl enable --now supervised-agent.service
systemctl enable --now supervised-agent-renew.timer
systemctl enable --now supervised-agent-healthcheck.timer

echo
echo "Installed."
echo "  systemctl status supervised-agent.service"
echo "  systemctl list-timers supervised-agent-renew.timer supervised-agent-healthcheck.timer"
echo "  journalctl -u supervised-agent.service -f"
echo
echo "Attach to the agent session:"
echo "  sudo -u $AGENT_USER tmux attach -t ${AGENT_SESSION_NAME:-supervised-agent}"
echo "  (Detach with Ctrl+B, D — the session keeps running.)"
