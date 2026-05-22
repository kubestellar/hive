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

# Block supervisor from creating issues (supervisor observes and reports only)
if [ "$subcmd" = "issue" ] && [ "$action" = "create" ]; then
  if [[ "${AGENT_ID:-}" == "supervisor" || "${HIVE_AGENT:-}" == "supervisor" ]]; then
    echo "⛔ BLOCKED: supervisor cannot create issues. Supervisor observes and reports only." >&2
    exit 1
  fi
fi

# ── ACMM level enforcement ──
# At L1/L2: block issue create, pr create, pr merge.
# Exception: commenting on the advisory issue is always allowed.
ACMM_LEVEL="${HIVE_ACMM_LEVEL:-0}"
ADVISORY_ISSUE="${HIVE_ADVISORY_ISSUE:-}"

if [ "$ACMM_LEVEL" -gt 0 ] && [ "$ACMM_LEVEL" -lt 3 ]; then
  if [ "$subcmd" = "issue" ] && [ "$action" = "create" ]; then
    # Extract --title and --body from args to save as advisory finding
    _adv_title="" _adv_body="" _next_is_title=false _next_is_body=false
    for arg in "${args[@]}"; do
      if $_next_is_title; then _adv_title="$arg"; _next_is_title=false; continue; fi
      if $_next_is_body;  then _adv_body="$arg";  _next_is_body=false;  continue; fi
      case "$arg" in
        --title)   _next_is_title=true ;;
        --title=*) _adv_title="${arg#--title=}" ;;
        --body)    _next_is_body=true ;;
        --body=*)  _adv_body="${arg#--body=}" ;;
        -t)        _next_is_title=true ;;
        -b)        _next_is_body=true ;;
      esac
    done
    # Write advisory finding to JSONL for governor digest
    ADVISORY_DIR="/data/advisory"
    mkdir -p "$ADVISORY_DIR"
    AGENT_NAME="${HIVE_AGENT:-unknown}"
    python3 -c "
import json, datetime, sys
f = {
    'agent': '${AGENT_NAME}',
    'timestamp': datetime.datetime.utcnow().isoformat() + 'Z',
    'type': 'issue',
    'severity': 'medium',
    'title': sys.argv[1],
    'detail': sys.argv[2][:500] if len(sys.argv[2]) > 500 else sys.argv[2]
}
with open('${ADVISORY_DIR}/${AGENT_NAME}.jsonl', 'a') as fh:
    fh.write(json.dumps(f) + '\n')
" "${_adv_title:-untitled}" "${_adv_body:-}" 2>/dev/null || true
    echo "⛔ BLOCKED: gh issue create is not allowed at ACMM L${ACMM_LEVEL}." >&2
    echo "L1/L2 agents are advisory-only. Finding saved to advisory digest." >&2
    if [ -n "$ADVISORY_ISSUE" ]; then
      echo "Finding will appear in advisory issue #${ADVISORY_ISSUE} at next governor cycle." >&2
    fi
    exit 1
  fi
  if [ "$subcmd" = "pr" ] && { [ "$action" = "create" ] || [ "$action" = "merge" ]; }; then
    echo "⛔ BLOCKED: gh pr ${action} is not allowed at ACMM L${ACMM_LEVEL}." >&2
    echo "L1/L2 agents are advisory-only. No PRs allowed." >&2
    exit 1
  fi
fi
# At L3: only quality agent can create issues and hold-gated PRs. All others are advisory.
# Merging is blocked for ALL agents at L3.
if [ "$ACMM_LEVEL" -eq 3 ]; then
  AGENT_NAME_L3="${HIVE_AGENT:-${HIVE_AGENT_ID:-unknown}}"
  if [ "$subcmd" = "pr" ] && [ "$action" = "merge" ]; then
    echo "⛔ BLOCKED: gh pr merge is not allowed at ACMM L3." >&2
    echo "L3 PRs are hold-gated — merging requires human approval." >&2
    exit 1
  fi
  if [ "$subcmd" = "issue" ] && [ "$action" = "create" ] && [ "$AGENT_NAME_L3" != "quality" ]; then
    _adv_title="" _adv_body="" _next_is_title=false _next_is_body=false
    for arg in "${args[@]}"; do
      if $_next_is_title; then _adv_title="$arg"; _next_is_title=false; continue; fi
      if $_next_is_body;  then _adv_body="$arg";  _next_is_body=false;  continue; fi
      case "$arg" in
        --title)   _next_is_title=true ;;
        --title=*) _adv_title="${arg#--title=}" ;;
        --body)    _next_is_body=true ;;
        --body=*)  _adv_body="${arg#--body=}" ;;
        -t)        _next_is_title=true ;;
        -b)        _next_is_body=true ;;
      esac
    done
    ADVISORY_DIR="/data/advisory"
    mkdir -p "$ADVISORY_DIR"
    python3 -c "
import json, datetime, sys
f = {
    'agent': '${AGENT_NAME_L3}',
    'timestamp': datetime.datetime.utcnow().isoformat() + 'Z',
    'type': 'issue',
    'severity': 'medium',
    'title': sys.argv[1],
    'detail': sys.argv[2][:500] if len(sys.argv[2]) > 500 else sys.argv[2]
}
with open('${ADVISORY_DIR}/${AGENT_NAME_L3}.jsonl', 'a') as fh:
    fh.write(json.dumps(f) + '\n')
" "${_adv_title:-untitled}" "${_adv_body:-}" 2>/dev/null || true
    echo "⛔ BLOCKED: only the quality agent can create issues at ACMM L3." >&2
    echo "Agent '${AGENT_NAME_L3}' is advisory-only at L3. Finding saved to advisory digest." >&2
    exit 1
  fi
  if [ "$subcmd" = "pr" ] && [ "$action" = "create" ] && [ "$AGENT_NAME_L3" != "quality" ]; then
    echo "⛔ BLOCKED: only the quality agent can create PRs at ACMM L3." >&2
    echo "Agent '${AGENT_NAME_L3}' is advisory-only at L3. No PRs allowed." >&2
    exit 1
  fi
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
AGENT_DISPLAY_NAME="${HIVE_AGENT_DISPLAY_NAME:-$AGENT_NAME}"
HIVE_INSTANCE_ID="${HIVE_ID:-}"
HIVE_SHA="${HIVE_SHA:-$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')}"

# Build identity footer for injection into issue/comment bodies.
_identity_footer() {
  local parts="---\n🐝 **Hive Agent**:"
  [[ -n "$AGENT_DISPLAY_NAME" ]] && parts="${parts} \`${AGENT_DISPLAY_NAME}\`"
  [[ -n "$HIVE_INSTANCE_ID" ]] && parts="${parts} | **Instance:** \`${HIVE_INSTANCE_ID}\`"
  parts="${parts} | **SHA:** \`${HIVE_SHA}\`"
  echo -e "$parts"
}

# Inject identity footer into --body argument if present, otherwise append --body.
_inject_identity() {
  local footer
  footer="$(_identity_footer)"
  local new_args=()
  local body_found=false
  local i=0
  while [ $i -lt ${#args[@]} ]; do
    if [ "${args[$i]}" = "--body" ] && [ $((i+1)) -lt ${#args[@]} ]; then
      new_args+=("--body")
      new_args+=("${args[$((i+1))]}
${footer}")
      body_found=true
      i=$((i+2))
    elif [[ "${args[$i]}" == --body=* ]]; then
      local body_val="${args[$i]#--body=}"
      new_args+=("--body=${body_val}
${footer}")
      body_found=true
      i=$((i+1))
    else
      new_args+=("${args[$i]}")
      i=$((i+1))
    fi
  done
  if ! $body_found; then
    new_args+=("--body" "${footer}")
  fi
  args=("${new_args[@]}")
}

if [[ -n "$AGENT_NAME" ]]; then
  LABELS_CSV="agent/${AGENT_DISPLAY_NAME}"
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
    "$REAL_GH" label create "agent/${AGENT_DISPLAY_NAME}" --description "Work by the ${AGENT_DISPLAY_NAME} agent" --color 6f42c1 $rf 2>/dev/null || true
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
      _inject_identity
      exec "$REAL_GH" "${args[@]}" --label "$LABELS_CSV"
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
      _inject_identity
      _extract_item
      "$REAL_GH" "${args[@]}"
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
