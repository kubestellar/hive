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
  gemini)
    CMD="gemini"
    PERM_FLAG="--yolo"
    MODEL_FLAG="--model"
    ;;
  codex)
    CMD="codex"
    PERM_FLAG="--full-auto"
    MODEL_FLAG="--model"
    ;;
  amazonq)
    CMD="q"
    PERM_FLAG="--trust-all-tools"
    MODEL_FLAG=""
    ;;
  goose)
    # Goose: open source, any backend via config (~/.config/goose/config.yaml)
    # Point it at ollama or litellm for local models, or cloud APIs.
    CMD="goose"
    PERM_FLAG="--no-confirm"
    MODEL_FLAG=""  # model set in ~/.config/goose/config.yaml
    ;;
  aider)
    # Aider: best local coding agent. Use with ollama or litellm proxy.
    # Set AIDER_BASE_URL=http://localhost:4000 to route through litellm.
    CMD="aider"
    PERM_FLAG="--yes"
    MODEL_FLAG="--model"
    ;;
  *)
    echo "Unknown backend: $BACKEND" >&2
    echo "Supported: claude, copilot, gemini, codex, amazonq, goose, aider" >&2
    exit 1
    ;;
esac

FULL_CMD=("$CMD" "$PERM_FLAG")
if [[ -n "$MODEL" ]]; then
  # Normalize model version format: claude uses hyphens (4-5), copilot uses dots (4.5)
  case "$BACKEND" in
    copilot)
      # claude-haiku-4-5 → claude-haiku-4.5 (last hyphen before final digit becomes dot)
      MODEL=$(echo "$MODEL" | sed -E 's/([0-9]+)-([0-9]+)$/\1.\2/')
      ;;
    claude)
      # claude-haiku-4.5 → claude-haiku-4-5 (dot in version becomes hyphen)
      MODEL=$(echo "$MODEL" | sed -E 's/([0-9]+)\.([0-9]+)$/\1-\2/')
      ;;
  esac
  FULL_CMD+=("$MODEL_FLAG" "$MODEL")
fi
if [[ ${#EXTRA_ARGS[@]} -gt 0 ]]; then
  FULL_CMD+=("${EXTRA_ARGS[@]}")
fi

exec "${FULL_CMD[@]}"
