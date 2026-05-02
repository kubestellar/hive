#!/bin/bash
# gh wrapper — enforces agent safety rules and injects App token.
# Installed at /usr/local/bin/gh (ahead of /usr/bin/gh in PATH).
# Agents must read from /var/run/hive-metrics/actionable.json for listings.

set -euo pipefail

REAL_GH="/usr/bin/gh"

# Inject GitHub App token for agent gh calls (15k/hr vs PAT's 5k/hr).
# HIVE_GITHUB_TOKEN is set by hive-config.sh (sourced via agent-launch.sh).
# Always override GH_TOKEN here — agent-launch.sh unsets it to protect Copilot
# CLI auth, but gh CLI still falls back to ~/.config/gh/hosts.yml PAT (5k limit).
# This per-call injection ensures gh uses the App token without polluting the
# agent's persistent env (Copilot CLI never calls this wrapper).
#
# Fallback: if HIVE_GITHUB_TOKEN is unset (agent launched without hive-config.sh),
# read from the token cache file written by gh-app-token.sh.
GH_APP_TOKEN_CACHE="/var/run/hive-metrics/gh-app-token.cache"
if [[ -z "${HIVE_GITHUB_TOKEN:-}" && -f "$GH_APP_TOKEN_CACHE" ]]; then
  HIVE_GITHUB_TOKEN=$(cat "$GH_APP_TOKEN_CACHE")
fi
if [[ -n "${HIVE_GITHUB_TOKEN:-}" ]]; then
  export GH_TOKEN="$HIVE_GITHUB_TOKEN"
fi

args=("$@")
subcmd=""
action=""
for arg in "${args[@]}"; do
  case "$arg" in
    -*) continue ;;
    *)
      if [ -z "$subcmd" ]; then
        subcmd="$arg"
      elif [ -z "$action" ]; then
        action="$arg"
        break
      fi
      ;;
  esac
done

# Block gh issue list and gh pr list
if { [ "$subcmd" = "issue" ] || [ "$subcmd" = "pr" ]; } && [ "$action" = "list" ]; then
  echo "⛔ BLOCKED: gh $subcmd list is disabled for agents." >&2
  echo "Read /var/run/hive-metrics/actionable.json instead." >&2
  exit 1
fi

# Block gh api calls that list issues or pulls (enumeration endpoints)
if [ "$subcmd" = "api" ]; then
  for arg in "${args[@]}"; do
    case "$arg" in
      repos/*/issues\?*|repos/*/issues|repos/*/pulls\?*|repos/*/pulls)
        echo "⛔ BLOCKED: gh api issue/PR listing is disabled for agents." >&2
        echo "Read /var/run/hive-metrics/actionable.json instead." >&2
        exit 1
        ;;
    esac
  done
fi

exec "$REAL_GH" "$@"
