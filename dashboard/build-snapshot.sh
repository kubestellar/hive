#!/usr/bin/env bash
# Build a static HTML snapshot of the hive dashboard.
# Fetches current state from the live dashboard API and bakes it into
# a self-contained HTML file that renders identically but without SSE.
#
# Usage: ./build-snapshot.sh [DASHBOARD_URL] [OUTPUT_FILE]
#   DASHBOARD_URL  defaults to http://localhost:3001
#   OUTPUT_FILE    defaults to ./snapshot.html

set -euo pipefail

DASHBOARD_URL="${1:-${HIVE_DASHBOARD_URL:-http://localhost:3001}}"
OUTPUT_FILE="${2:-snapshot.html}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

node "${SCRIPT_DIR}/build-snapshot.mjs" "$DASHBOARD_URL" "$OUTPUT_FILE"
