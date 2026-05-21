#!/bin/bash
# Container entrypoint — starts dashboard + supervisor together.
# The dashboard runs in the background; supervisor is the foreground process
# so Docker tracks its lifecycle for health checks and restarts.
set -euo pipefail

DASHBOARD_DIR="/opt/hive/dashboard"
DASHBOARD_LOG="/data/logs/dashboard.log"

if [ -f "$DASHBOARD_DIR/server.js" ] && [ -f "$DASHBOARD_DIR/node_modules/.package-lock.json" ]; then
  echo "[entrypoint] starting dashboard server"
  cd "$DASHBOARD_DIR"
  node server.js >> "$DASHBOARD_LOG" 2>&1 &
  DASHBOARD_PID=$!
  echo "[entrypoint] dashboard PID=$DASHBOARD_PID"
  cd /data
else
  echo "[entrypoint] dashboard not available (missing server.js or node_modules)"
fi

exec /opt/hive/bin/supervisor.sh "$@"
