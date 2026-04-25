#!/bin/bash
# notify.sh - shared notification library for hive scripts.
#
# Source this file, then call: notify "<title>" "<body>" [priority]
#
# Reads from environment (set in /etc/hive/hive.conf or governor.env):
#   NTFY_TOPIC      - ntfy.sh topic (free push to phone/desktop)
#   NTFY_SERVER     - ntfy server (default: https://ntfy.sh)
#   SLACK_WEBHOOK   - Slack incoming webhook URL
#   DISCORD_WEBHOOK - Discord webhook URL
#
# Priority: "default" | "high" | "low"

notify() {
  local title="${1:-}"
  local body="${2:-}"
  local priority="${3:-default}"

  # -- ntfy --
  if [[ -n "${NTFY_TOPIC:-}" ]]; then
    local server="${NTFY_SERVER:-https://ntfy.sh}"
    local pri_flag=()
    [[ "$priority" == "high" ]] && pri_flag=(-H "Priority: urgent")
    [[ "$priority" == "low"  ]] && pri_flag=(-H "Priority: low")
    curl -s "${pri_flag[@]}" \
      -H "Title: ${title}" \
      -d "${body}" \
      "${server}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
  fi

  # -- Slack --
  if [[ -n "${SLACK_WEBHOOK:-}" ]]; then
    local prefix=""
    [[ "$priority" == "high" ]] && prefix="WARNING "
    local escaped_title escaped_body
    escaped_title=$(printf '%s' "${prefix}${title}" | sed 's/"/\\"/g')
    escaped_body=$(printf '%s' "${body}" | sed 's/"/\\"/g')
    curl -s -X POST \
      -H "Content-type: application/json" \
      --data "{\"text\":\"*${escaped_title}*\n${escaped_body}\"}" \
      "${SLACK_WEBHOOK}" >/dev/null 2>&1 || true
  fi

  # -- Discord --
  if [[ -n "${DISCORD_WEBHOOK:-}" ]]; then
    local prefix=""
    [[ "$priority" == "high" ]] && prefix="WARNING "
    local escaped_title escaped_body
    escaped_title=$(printf '%s' "${prefix}${title}" | sed 's/"/\\"/g')
    escaped_body=$(printf '%s' "${body}" | sed 's/"/\\"/g')
    curl -s -X POST \
      -H "Content-type: application/json" \
      --data "{\"content\":\"**${escaped_title}**\n${escaped_body}\"}" \
      "${DISCORD_WEBHOOK}/slack" >/dev/null 2>&1 || true
  fi
}
