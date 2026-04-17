#!/bin/bash
# install.sh — install supervised-agent scripts + systemd units.
# Run as root. Two modes:
#
#   sudo ./install.sh                       # single-instance (back-compat)
#   sudo ./install.sh --instance <name>     # named instance for multi-agent
#
# Single-instance uses /etc/supervised-agent/agent.env and the non-templated
# units (supervised-agent.service etc.). Named instance uses the templated
# units (supervised-agent@<name>.service) and /etc/supervised-agent/<name>.env.
# You can mix both on the same host — each call installs what it needs.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"

INSTANCE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --instance)
      INSTANCE="$2"
      shift 2
      ;;
    --instance=*)
      INSTANCE="${1#--instance=}"
      shift
      ;;
    *)
      echo "unknown flag: $1" >&2
      echo "usage: $0 [--instance <name>]" >&2
      exit 1
      ;;
  esac
done

if [ -n "$INSTANCE" ]; then
  ENV_FILE="/etc/supervised-agent/${INSTANCE}.env"
  UNIT_SUPERVISOR="supervised-agent@${INSTANCE}.service"
  UNIT_RENEW_TIMER="supervised-agent-renew@${INSTANCE}.timer"
  UNIT_HEALTH_TIMER="supervised-agent-healthcheck@${INSTANCE}.timer"
  TEMPLATED=1
else
  ENV_FILE="/etc/supervised-agent/agent.env"
  UNIT_SUPERVISOR="supervised-agent.service"
  UNIT_RENEW_TIMER="supervised-agent-renew.timer"
  UNIT_HEALTH_TIMER="supervised-agent-healthcheck.timer"
  TEMPLATED=0
fi

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

if [ "$TEMPLATED" = 1 ]; then
  UNITS=(
    "supervised-agent@.service"
    "supervised-agent-renew@.service"
    "supervised-agent-renew@.timer"
    "supervised-agent-healthcheck@.service"
    "supervised-agent-healthcheck@.timer"
  )
else
  UNITS=(
    "supervised-agent.service"
    "supervised-agent-renew.service"
    "supervised-agent-renew.timer"
    "supervised-agent-healthcheck.service"
    "supervised-agent-healthcheck.timer"
  )
fi

echo "==> installing systemd units to $SYSTEMD_DIR (User=$AGENT_USER)"
for unit in "${UNITS[@]}"; do
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
systemctl enable --now "$UNIT_SUPERVISOR"
systemctl enable --now "$UNIT_RENEW_TIMER"
systemctl enable --now "$UNIT_HEALTH_TIMER"

echo
echo "Installed."
echo "  systemctl status $UNIT_SUPERVISOR"
echo "  systemctl list-timers $UNIT_RENEW_TIMER $UNIT_HEALTH_TIMER"
echo "  journalctl -u $UNIT_SUPERVISOR -f"
echo
echo "Attach to the agent session:"
echo "  sudo -u $AGENT_USER tmux attach -t ${AGENT_SESSION_NAME:-supervised-agent}"
echo "  (Detach with Ctrl+B, D — the session keeps running.)"
