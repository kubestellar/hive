#!/bin/bash
# gh wrapper — enforces per-agent + global restrictions and injects App token.
# Installed at /usr/local/bin/gh (ahead of /usr/bin/gh in PATH).
#
# Per-agent restrictions live at /etc/hive/restrictions/<agent-id>.json.
# The wrapper reads HIVE_AGENT_ID to find the right file.
#
# Restriction file format:
#   { "rules": [
#       { "pattern": "gh issue list*", "reason": "Use actionable.json", "enabled": true },
#       { "pattern": "gh api repos/*/issues*", "reason": "Enumeration disabled", "enabled": true }
#   ]}
#
# Pattern matching: the full command ("gh issue list --repo foo") is checked
# against each pattern using bash glob matching. Patterns support * wildcards.

set -euo pipefail

REAL_GH="/usr/bin/gh"
RESTRICTIONS_DIR="/etc/hive/restrictions"

# Inject GitHub App token for agent gh calls (15k/hr vs PAT's 5k/hr).
GH_APP_TOKEN_CACHE="/var/run/hive-metrics/gh-app-token.cache"
if [[ -f "$GH_APP_TOKEN_CACHE" ]]; then
  export GH_TOKEN="$(cat "$GH_APP_TOKEN_CACHE")"
elif [[ -n "${HIVE_GITHUB_TOKEN:-}" ]]; then
  export GH_TOKEN="$HIVE_GITHUB_TOKEN"
fi

# Build the full command string for pattern matching
FULL_CMD="gh $*"

# Check per-agent restrictions
AGENT_ID="${HIVE_AGENT_ID:-}"
if [[ -n "$AGENT_ID" ]]; then
  RESTRICTION_FILE="${RESTRICTIONS_DIR}/${AGENT_ID}.json"
  if [[ -f "$RESTRICTION_FILE" ]]; then
    while IFS='|' read -r pattern reason; do
      [[ -z "$pattern" ]] && continue
      # Use bash extglob for pattern matching
      # shellcheck disable=SC2254
      case "$FULL_CMD" in
        $pattern)
          echo "⛔ BLOCKED: ${reason:-command not allowed for ${AGENT_ID}}" >&2
          exit 1
          ;;
      esac
    done < <(python3 -c "
import json, sys
try:
    with open('${RESTRICTION_FILE}') as f:
        data = json.load(f)
    for r in data.get('rules', []):
        if r.get('enabled', True):
            print(r.get('pattern','') + '|' + r.get('reason',''))
except Exception:
    pass
" 2>/dev/null)
  fi
fi

# Global defaults — always enforced for all agents regardless of restriction file
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

# Block gh issue list and gh pr list (global)
if { [ "$subcmd" = "issue" ] || [ "$subcmd" = "pr" ]; } && [ "$action" = "list" ]; then
  echo "⛔ BLOCKED: gh $subcmd list is disabled for agents." >&2
  echo "Read /var/run/hive-metrics/actionable.json instead." >&2
  exit 1
fi

# Enforce merge gate — only PRs in merge-eligible.json can be merged
MERGE_ELIGIBLE_FILE="/var/run/hive-metrics/merge-eligible.json"
if [ "$subcmd" = "pr" ] && [ "$action" = "merge" ]; then
  pr_num=""
  pr_repo=""
  skip_next=false
  past_merge=false
  for arg in "${args[@]}"; do
    if $skip_next; then skip_next=false; continue; fi
    case "$arg" in
      pr|merge) past_merge=true; continue ;;
      --repo) skip_next=true; continue ;;
      --repo=*) pr_repo="${arg#--repo=}"; continue ;;
      -*) continue ;;
      *)
        if $past_merge && [ -z "$pr_num" ]; then
          pr_num="$arg"
        fi
        ;;
    esac
  done

  if [ -z "$pr_repo" ]; then
    for i in "${!args[@]}"; do
      if [ "${args[$i]}" = "--repo" ] && [ -n "${args[$((i+1))]:-}" ]; then
        pr_repo="${args[$((i+1))]}"
        break
      fi
    done
  fi

  if [ -n "$pr_num" ] && [ -f "$MERGE_ELIGIBLE_FILE" ]; then
    is_eligible=$(python3 -c "
import json, sys
try:
    with open('${MERGE_ELIGIBLE_FILE}') as f:
        data = json.load(f)
    repo_filter = '${pr_repo}'
    for pr in data.get('merge_eligible', []):
        if str(pr.get('number')) == '${pr_num}':
            if not repo_filter or pr.get('repo','') == repo_filter:
                print('yes')
                sys.exit(0)
    print('no')
except Exception as e:
    print('error:' + str(e), file=sys.stderr)
    print('no')
" 2>/dev/null)

    if [ "$is_eligible" != "yes" ]; then
      echo "⛔ BLOCKED: PR #${pr_num} is NOT in merge-eligible.json." >&2
      echo "The merge gate requires all CI checks to pass before merging." >&2
      echo "Run 'cat ${MERGE_ELIGIBLE_FILE} | python3 -m json.tool' to see eligible PRs." >&2
      exit 1
    fi
  elif [ -n "$pr_num" ] && [ ! -f "$MERGE_ELIGIBLE_FILE" ]; then
    echo "⛔ BLOCKED: ${MERGE_ELIGIBLE_FILE} not found — cannot verify merge eligibility." >&2
    echo "Run merge-gate.sh first, or wait for the next pipeline cycle." >&2
    exit 1
  fi
fi

# Block gh api calls that list issues or pulls (global)
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

# Auto-label issues and PRs with agent identity + hive instance ID.
# HIVE_AGENT is set by the Go binary (e.g. "scanner").
# HIVE_ID is the unique hive instance ID (e.g. "hive-bold-fox").
AGENT_NAME="${HIVE_AGENT:-$AGENT_ID}"
HIVE_INSTANCE_ID="${HIVE_ID:-}"

if [[ -n "$AGENT_NAME" ]]; then
  LABELS_CSV="agent/${AGENT_NAME}"
  [[ -n "$HIVE_INSTANCE_ID" ]] && LABELS_CSV="${LABELS_CSV},hive/${HIVE_INSTANCE_ID}"

  # Ensure labels exist on the repo (cached per-session to avoid repeated API calls).
  LABEL_CACHE="/tmp/.hive-labels-ensured"
  _ensure_labels() {
    [[ -f "$LABEL_CACHE" ]] && return 0
    local repo_flag=""
    for arg in "${args[@]}"; do
      case "$arg" in
        --repo) repo_flag="next" ;;
        --repo=*) repo_flag="${arg#--repo=}" ; break ;;
        *) [[ "$repo_flag" = "next" ]] && repo_flag="$arg" && break ;;
      esac
    done
    [[ "$repo_flag" = "next" ]] && repo_flag=""
    local rf=""
    [[ -n "$repo_flag" ]] && rf="--repo $repo_flag"
    "$REAL_GH" label create "agent/${AGENT_NAME}" --description "Work by the ${AGENT_NAME} agent" --color 6f42c1 $rf 2>/dev/null || true
    if [[ -n "$HIVE_INSTANCE_ID" ]]; then
      "$REAL_GH" label create "hive/${HIVE_INSTANCE_ID}" --description "Hive instance ${HIVE_INSTANCE_ID}" --color 1d76db $rf 2>/dev/null || true
    fi
    touch "$LABEL_CACHE"
  }

  # Extract issue/PR number and repo from args (for post-action labeling).
  _extract_item() {
    item_num=""
    item_repo=""
    local skip=false
    for arg in "${args[@]}"; do
      if $skip; then skip=false; item_repo="$arg"; continue; fi
      case "$arg" in
        comment|review|"$subcmd"|"$action") continue ;;
        --repo) skip=true; continue ;;
        --repo=*) item_repo="${arg#--repo=}"; continue ;;
        -*) continue ;;
        *) [[ -z "$item_num" ]] && item_num="$arg" ;;
      esac
    done
  }

  case "$subcmd/$action" in
    issue/create|pr/create)
      _ensure_labels
      exec "$REAL_GH" "$@" --label "$LABELS_CSV"
      ;;
    issue/edit|pr/edit)
      _ensure_labels
      exec "$REAL_GH" "$@" --add-label "$LABELS_CSV"
      ;;
    pr/merge)
      _ensure_labels
      _extract_item
      if [[ -n "$item_num" ]]; then
        local_repo=""
        [[ -n "$item_repo" ]] && local_repo="--repo $item_repo"
        "$REAL_GH" pr edit "$item_num" $local_repo --add-label "$LABELS_CSV" 2>/dev/null || true
      fi
      exec "$REAL_GH" "$@"
      ;;
    issue/comment|pr/comment|pr/review)
      _ensure_labels
      _extract_item
      "$REAL_GH" "$@"
      exit_code=$?
      if [[ $exit_code -eq 0 && -n "$item_num" ]]; then
        local_repo=""
        [[ -n "$item_repo" ]] && local_repo="--repo $item_repo"
        "$REAL_GH" "$subcmd" edit "$item_num" $local_repo --add-label "$LABELS_CSV" 2>/dev/null || true
      fi
      exit $exit_code
      ;;
  esac
fi

exec "$REAL_GH" "$@"
