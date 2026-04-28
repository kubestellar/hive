#!/bin/bash
# token-collector.sh — correlates bead data with token usage for per-issue cost attribution
# Reads scanner beads and token metrics to produce per-issue cost data.
#
# Output: JSON array to stdout, also written to /var/run/hive-metrics/issue-costs.json
#
# Usage:
#   token-collector.sh              # all closed beads from last 24h
#   token-collector.sh --since 48h  # closed beads from last 48h
#   token-collector.sh --all        # all closed beads ever

set -euo pipefail

BEADS_DIR="${BEADS_DIR:-/home/dev/scanner-beads}"
METRICS_DIR="${METRICS_DIR:-/var/run/hive-metrics}"
OUTPUT_FILE="${METRICS_DIR}/issue-costs.json"
SINCE_HOURS=24

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --since) SINCE_HOURS="${2%h}"; shift 2 ;;
    --all) SINCE_HOURS=0; shift ;;
    *) shift ;;
  esac
done

# Ensure output dir exists
mkdir -p "$METRICS_DIR" 2>/dev/null || true

# Get closed beads from scanner.
# bd list returns JSON with: id, title, type, status, priority, external_ref,
# metadata (contains pr_ref, session_tokens, etc.), created_at, updated_at, closed_at
get_closed_beads() {
  if ! command -v bd &>/dev/null; then
    echo "[]"
    return
  fi

  if [[ ! -d "$BEADS_DIR" ]] && [[ ! -d "$BEADS_DIR/.beads" ]]; then
    echo "[]"
    return
  fi

  local beads_json
  beads_json=$(cd "$BEADS_DIR" && bd list --status=closed --actor=scanner --json 2>/dev/null || echo "[]")

  if [[ "$SINCE_HOURS" -gt 0 ]]; then
    local cutoff_epoch
    # Support both GNU date (-d) and BSD date (-v)
    cutoff_epoch=$(date -d "-${SINCE_HOURS} hours" +%s 2>/dev/null \
      || date -v-${SINCE_HOURS}H +%s 2>/dev/null \
      || echo 0)
    echo "$beads_json" | jq --argjson cutoff "$cutoff_epoch" \
      '[.[] | select(
         ((.closed_at // .updated_at // "1970-01-01T00:00:00Z") | fromdateiso8601) >= $cutoff
       )]'
  else
    echo "$beads_json"
  fi
}

# Get aggregated token usage for scanner from the tokens.json metrics file.
# Returns the total tokens attributed to the scanner agent in the lookback window.
get_scanner_tokens() {
  local tokens_file="${METRICS_DIR}/tokens.json"
  if [[ ! -f "$tokens_file" ]]; then
    echo 0
    return
  fi
  # Extract scanner's total from byAgent (input + output + cacheRead)
  local scanner_total
  scanner_total=$(jq -r '
    (.byAgent.scanner // {}) |
    ((.input // 0) + (.output // 0) + (.cacheRead // 0))
  ' "$tokens_file" 2>/dev/null || echo 0)
  echo "${scanner_total:-0}"
}

# Build per-issue cost array by correlating closed beads with token data.
# If a bead carries metadata.session_tokens it is used for exact attribution;
# otherwise the scanner's total tokens are divided evenly across all closed beads
# as an estimate.
build_issue_costs() {
  local beads_json
  beads_json=$(get_closed_beads)

  local total_beads
  total_beads=$(echo "$beads_json" | jq 'length')

  if [[ "$total_beads" -eq 0 ]]; then
    echo "[]"
    return
  fi

  # Average token cost per issue — used when per-bead session tracking is absent
  local total_tokens avg_per_issue
  total_tokens=$(get_scanner_tokens)
  if [[ "$total_beads" -gt 0 ]] && [[ "$total_tokens" -gt 0 ]]; then
    avg_per_issue=$(( total_tokens / total_beads ))
  else
    avg_per_issue=0
  fi

  # Produce one JSON entry per closed bead.
  # Fields:
  #   issue            — GitHub issue number (string, stripped of "gh-" prefix)
  #   pr               — PR number from metadata.pr_ref, or null
  #   title            — bead title
  #   tokens_estimated — avg tokens/issue (fallback when no per-session data)
  #   tokens_exact     — exact tokens if metadata.session_tokens was recorded
  #   tokens           — canonical value: exact if available, else estimated
  #   closed_at        — ISO timestamp when the bead was closed
  #   agent            — actor name (usually "scanner")
  echo "$beads_json" | jq --argjson avg "$avg_per_issue" '
    [.[] | {
      issue: (.external_ref // "unknown" | ltrimstr("gh-")),
      pr: (.metadata.pr_ref // null),
      title: (.title // "untitled"),
      tokens_estimated: $avg,
      tokens_exact: (if .metadata.session_tokens then (.metadata.session_tokens | tonumber) else null end),
      tokens: (if .metadata.session_tokens then (.metadata.session_tokens | tonumber) else $avg end),
      closed_at: (.closed_at // .updated_at // null),
      agent: (.actor // "scanner")
    }]
  '
}

# Main
RESULT=$(build_issue_costs)

# Write to file (best-effort — /var/run may not exist on all machines)
if echo "$RESULT" | jq '.' > "$OUTPUT_FILE" 2>/dev/null; then
  : # success
fi

# Print to stdout
echo "$RESULT" | jq '.'

# Summary to stderr
TOTAL=$(echo "$RESULT" | jq 'length')
TOTAL_TOKENS=$(echo "$RESULT" | jq '[.[].tokens] | add // 0')
echo "token-collector: $TOTAL issues, ~${TOTAL_TOKENS} tokens total" >&2
