#!/bin/bash
# agent-launch.sh — Unified launcher for any AI coding CLI backend.
#
# Supported backends: claude, copilot (add more in the case block below)
#
# Usage (in .env files):
#   AGENT_LAUNCH_CMD="agent-launch.sh --backend copilot --model claude-opus-4-6"
#   AGENT_LAUNCH_CMD="agent-launch.sh --backend claude --model claude-opus-4-6"
#
# Or override with env vars:
#   AGENT_BACKEND=copilot AGENT_MODEL=claude-opus-4-6 agent-launch.sh
#
# Adding a new backend:
#   1. Add a case block below with CMD, PERM_FLAG, MODEL_FLAG
#   2. Add idle prompt pattern to BACKENDS.md
#   3. Update kick-agents.sh session_idle() if prompt differs

set -euo pipefail

# Source hive-config.sh to make HIVE_GITHUB_TOKEN available for gh wrapper.
# Do NOT export GH_TOKEN here — Copilot CLI uses GH_TOKEN for its own Copilot
# API auth, which rejects GitHub App server-to-server tokens. The gh wrapper
# (/usr/local/bin/gh) injects HIVE_GITHUB_TOKEN on a per-call basis instead.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HIVE_CONFIG="${SCRIPT_DIR}/hive-config.sh"
if [[ -f "$HIVE_CONFIG" ]]; then
  source "$HIVE_CONFIG"
elif [[ -f /usr/local/bin/hive-config.sh ]]; then
  source /usr/local/bin/hive-config.sh
fi
unset GH_TOKEN

# Source the centralized backend/model config
BACKENDS_CONF="${SCRIPT_DIR}/../config/backends.conf"
if [[ -f "$BACKENDS_CONF" ]]; then
  # shellcheck source=../config/backends.conf
  source "$BACKENDS_CONF"
elif [[ -f /usr/local/etc/hive/backends.conf ]]; then
  source /usr/local/etc/hive/backends.conf
else
  echo "FATAL: backends.conf not found" >&2
  exit 1
fi

BACKEND="${AGENT_BACKEND:-claude}"
MODEL="${AGENT_MODEL:-}"
EXTRA_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --backend)  BACKEND="$2"; shift 2 ;;
    --model)    MODEL="$2"; shift 2 ;;
    *)          EXTRA_ARGS+=("$1"); shift ;;
  esac
done

CMD=$(backend_binary "$BACKEND")
PERM_FLAG=$(backend_perm_flag "$BACKEND")
MODEL_FLAG="--model"

# amazonq and goose don't support --model
case "$BACKEND" in
  amazonq|goose) MODEL_FLAG="" ;;
esac

if [[ -z "$CMD" || -z "$PERM_FLAG" ]]; then
  echo "Unknown backend: $BACKEND" >&2
  echo "Supported: $KNOWN_BACKENDS" >&2
  exit 1
fi

FULL_CMD=("$CMD" "$PERM_FLAG")
if [[ -n "$MODEL" && -n "$MODEL_FLAG" ]]; then
  MODEL=$(normalize_model_for_backend "$BACKEND" "$MODEL")
  FULL_CMD+=("$MODEL_FLAG" "$MODEL")
fi
if [[ ${#EXTRA_ARGS[@]} -gt 0 ]]; then
  FULL_CMD+=("${EXTRA_ARGS[@]}")
fi

exec "${FULL_CMD[@]}"
