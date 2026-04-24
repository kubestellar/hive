#!/bin/bash
# agent-launch.sh — Unified launcher for any AI coding CLI backend.
#
# Supported backends: claude, copilot (add more in the case block below)
#
# Usage (in .env files):
#   AGENT_LAUNCH_CMD="agent-launch.sh --backend copilot --model claude-opus-4.6"
#   AGENT_LAUNCH_CMD="agent-launch.sh --backend claude --model claude-opus-4-6"
#
# Or override with env vars:
#   AGENT_BACKEND=copilot AGENT_MODEL=claude-opus-4.6 agent-launch.sh
#
# Adding a new backend:
#   1. Add a case block below with CMD, PERM_FLAG, MODEL_FLAG
#   2. Add idle prompt pattern to BACKENDS.md
#   3. Update kick-agents.sh session_idle() if prompt differs

set -euo pipefail

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

# ── Backend definitions ──────────────────────────────────────────────
# To add a new backend:
#   CMD          = binary name or path
#   PERM_FLAG    = flag to bypass all permission prompts
#   MODEL_FLAG   = flag to select model (empty if backend doesn't support it)
#   RENAME_CMD   = slash command to rename session (empty if unsupported)
#   IDLE_PROMPT  = regex for idle detection (used by kick-agents.sh)

case "$BACKEND" in
  claude)
    CMD="claude"
    PERM_FLAG="--dangerously-skip-permissions"
    MODEL_FLAG="--model"
    ;;
  copilot)
    CMD="copilot"
    PERM_FLAG="--allow-all"
    MODEL_FLAG="--model"
    ;;
  # ── Add new backends here ──
  # aider)
  #   CMD="aider"
  #   PERM_FLAG="--yes"
  #   MODEL_FLAG="--model"
  #   ;;
  *)
    echo "Unknown backend: $BACKEND" >&2
    echo "Supported: claude, copilot" >&2
    exit 1
    ;;
esac

FULL_CMD=("$CMD" "$PERM_FLAG")
if [[ -n "$MODEL" ]]; then
  FULL_CMD+=("$MODEL_FLAG" "$MODEL")
fi
if [[ ${#EXTRA_ARGS[@]} -gt 0 ]]; then
  FULL_CMD+=("${EXTRA_ARGS[@]}")
fi

exec "${FULL_CMD[@]}"
