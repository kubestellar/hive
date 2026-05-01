#!/bin/bash
# gh wrapper — enforces agent safety rules and injects App token.
# Installed at /usr/local/bin/gh (ahead of /usr/bin/gh in PATH).
# Agents must read from /var/run/hive-metrics/actionable.json for listings.

REAL_GH="/usr/bin/gh"

# Inject GitHub App token for agent gh calls when available.
# HIVE_GITHUB_TOKEN is set by hive-config.sh (sourced via agent-launch.sh).
# GH_TOKEN is NOT set in the agent env (Copilot CLI uses it for its own auth),
# so we set it here per-call so gh CLI uses the App's 15k/hr rate limit pool.
if [[ -n "${HIVE_GITHUB_TOKEN:-}" && -z "${GH_TOKEN:-}" ]]; then
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
