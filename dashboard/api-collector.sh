#!/usr/bin/env bash
# Centralized GitHub API collector — single script fetches all data, writes to cache.
# All consumers (governor, dashboard, hive status) read from cache instead of calling
# the GitHub API independently. Reduces ~340 API calls/hour to ~35.
#
# Output: /var/run/hive-metrics/github-cache.json
# Called by: server.js on a 5-minute cycle

set -euo pipefail

CACHE_DIR="${HIVE_METRICS_DIR:-/var/run/hive-metrics}"
CACHE_FILE="$CACHE_DIR/github-cache.json"
CACHE_TMP="$CACHE_DIR/github-cache.tmp.$$"
mkdir -p "$CACHE_DIR" 2>/dev/null || true

# Source project config if available
if [[ -f /usr/local/bin/hive-config.sh ]]; then
  source /usr/local/bin/hive-config.sh 2>/dev/null || true
fi

REPOS_STR="${HIVE_REPOS:-${PROJECT_REPOS:-kubestellar/console kubestellar/console-marketplace kubestellar/docs kubestellar/homebrew-tap kubestellar/console-kb}}"
IFS=' ' read -ra REPOS <<< "$REPOS_STR"

PRIMARY_REPO="${PROJECT_PRIMARY_REPO:-kubestellar/console}"
AI_AUTHOR="${PROJECT_AI_AUTHOR:-clubanderson}"
PROJECT_ORG="${PROJECT_ORG:-kubestellar}"
PROJECT="${PROJECT_NAME:-KubeStellar}"

EXEMPT_LABEL_REGEX="nightly-tests|LFX|do-not-merge|meta-tracker|auto-qa-tuning-report|hold|adopters|changes-requested|waiting-on-author"

# Use real gh binary — the /usr/local/bin/gh wrapper blocks listing commands
# (designed for agents) which would break this infrastructure script.
GH=/usr/bin/gh

# Auth: use HIVE_GITHUB_TOKEN if set
if [ -n "${HIVE_GITHUB_TOKEN:-}" ]; then
  unset GITHUB_TOKEN 2>/dev/null || true
  export GH_TOKEN="$HIVE_GITHUB_TOKEN"
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# ── Fetch per-repo issue + PR counts (parallel, using gh issue/pr list = GraphQL) ──
for repo in "${REPOS[@]}"; do
  rname="${repo##*/}"
  (
    issues=$($GH issue list --repo "$repo" --state open --json number --jq 'length' 2>/dev/null || echo "-1")
    prs=$($GH pr list --repo "$repo" --state open --json number --jq 'length' 2>/dev/null || echo "-1")
    echo "${issues} ${prs}" > "$tmpdir/repo_${rname}"
  ) &
done

# ── Fetch actionable counts (issues excluding exempt labels) ──
for repo in "${REPOS[@]}"; do
  rname="${repo##*/}"
  (
    # Use gh search which is GraphQL-backed and more reliable than REST /issues
    actionable_issues=$($GH issue list --repo "$repo" --state open --json "number,labels" \
      --jq "[.[] | select(.labels | map(.name) | any(test(\"${EXEMPT_LABEL_REGEX}\"; \"i\")) | not)] | length" 2>/dev/null || echo "-1")
    actionable_prs=$($GH pr list --repo "$repo" --state open --json "number,labels" \
      --jq "[.[] | select(.labels | map(.name) | any(test(\"${EXEMPT_LABEL_REGEX}\"; \"i\")) | not)] | length" 2>/dev/null || echo "-1")
    echo "${actionable_issues} ${actionable_prs}" > "$tmpdir/actionable_${rname}"
  ) &
done

# ── Fetch primary repo metadata (stars, forks, contributors, adopters) ──
($GH api "repos/${PRIMARY_REPO}" --jq '.stargazers_count' > "$tmpdir/stars" 2>/dev/null || echo 0 > "$tmpdir/stars") &
($GH api "repos/${PRIMARY_REPO}" --jq '.forks_count' > "$tmpdir/forks" 2>/dev/null || echo 0 > "$tmpdir/forks") &
(
  c=$($GH api "repos/${PRIMARY_REPO}/contributors?per_page=1" -i 2>/dev/null | grep -oP 'page=\K\d+(?=>; rel="last")' || echo 0)
  [ "$c" = "0" ] && c=$($GH api "repos/${PRIMARY_REPO}/contributors" --jq 'length' 2>/dev/null || echo 0)
  echo "$c" > "$tmpdir/contribs"
) &
(
  a=$($GH api "repos/${PRIMARY_REPO}/contents/ADOPTERS.MD" --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -cP '^\|.*\|.*\|' || echo 0)
  a=$(( a > 2 ? a - 2 : 0 ))
  echo "$a" > "$tmpdir/adopters"
) &

# ── Fetch ACMM badge count (kubestellar-specific) ──
if [ "${PROJECT_ORG}" = "kubestellar" ]; then
  ($GH api repos/kubestellar/docs/contents/src/app/%5Blocale%5D/acmm-leaderboard/page.tsx --jq '.content' 2>/dev/null \
    | base64 -d 2>/dev/null \
    | sed -n '/BADGE_PARTICIPANTS = new Set/,/\]);/p' \
    | grep -cP '^\s+"[a-zA-Z]' > "$tmpdir/acmm" 2>/dev/null || echo 0 > "$tmpdir/acmm") &
else
  echo 0 > "$tmpdir/acmm" &
fi

# ── Fetch outreach PR counts ──
($GH api "search/issues?q=author:${AI_AUTHOR}+type:pr+is:open+${PROJECT}+in:title+-org:${PROJECT_ORG}" --jq '.total_count' > "$tmpdir/outreach_open" 2>/dev/null || echo 0 > "$tmpdir/outreach_open") &
($GH api "search/issues?q=author:${AI_AUTHOR}+type:pr+is:merged+${PROJECT}+in:title+-org:${PROJECT_ORG}" --jq '.total_count' > "$tmpdir/outreach_merged" 2>/dev/null || echo 0 > "$tmpdir/outreach_merged") &

wait

# ── Assemble JSON ──
repos_json="["
first=true
total_issues=0
total_prs=0
total_actionable_issues=0
total_actionable_prs=0

for repo in "${REPOS[@]}"; do
  rname="${repo##*/}"

  raw=$(cat "$tmpdir/repo_${rname}" 2>/dev/null || echo "-1 -1")
  issues="${raw%% *}"
  prs="${raw##* }"

  araw=$(cat "$tmpdir/actionable_${rname}" 2>/dev/null || echo "-1 -1")
  ai="${araw%% *}"
  ap="${araw##* }"

  # Fall back to previous cache if API failed
  if [[ "$issues" == "-1" ]] && [[ -f "$CACHE_FILE" ]]; then
    issues=$(jq -r ".repos[] | select(.name == \"$rname\") | .issues // 0" "$CACHE_FILE" 2>/dev/null || echo 0)
  fi
  [[ "$issues" == "-1" ]] && issues=0

  if [[ "$prs" == "-1" ]] && [[ -f "$CACHE_FILE" ]]; then
    prs=$(jq -r ".repos[] | select(.name == \"$rname\") | .prs // 0" "$CACHE_FILE" 2>/dev/null || echo 0)
  fi
  [[ "$prs" == "-1" ]] && prs=0

  if [[ "$ai" == "-1" ]] && [[ -f "$CACHE_FILE" ]]; then
    ai=$(jq -r ".repos[] | select(.name == \"$rname\") | .actionableIssues // 0" "$CACHE_FILE" 2>/dev/null || echo 0)
  fi
  [[ "$ai" == "-1" ]] && ai=0

  if [[ "$ap" == "-1" ]] && [[ -f "$CACHE_FILE" ]]; then
    ap=$(jq -r ".repos[] | select(.name == \"$rname\") | .actionablePrs // 0" "$CACHE_FILE" 2>/dev/null || echo 0)
  fi
  [[ "$ap" == "-1" ]] && ap=0

  total_issues=$(( total_issues + issues ))
  total_prs=$(( total_prs + prs ))
  total_actionable_issues=$(( total_actionable_issues + ai ))
  total_actionable_prs=$(( total_actionable_prs + ap ))

  [[ "$first" == "true" ]] && first=false || repos_json+=","
  repos_json+="{\"name\":\"$rname\",\"full\":\"$repo\",\"issues\":$issues,\"prs\":$prs,\"actionableIssues\":$ai,\"actionablePrs\":$ap}"
done
repos_json+="]"

stars=$(cat "$tmpdir/stars" 2>/dev/null || echo 0)
forks=$(cat "$tmpdir/forks" 2>/dev/null || echo 0)
contributors=$(cat "$tmpdir/contribs" 2>/dev/null || echo 0)
adopters=$(cat "$tmpdir/adopters" 2>/dev/null || echo 0)
acmm=$(cat "$tmpdir/acmm" 2>/dev/null || echo 0)
outreach_open=$(cat "$tmpdir/outreach_open" 2>/dev/null || echo 0)
outreach_merged=$(cat "$tmpdir/outreach_merged" 2>/dev/null || echo 0)

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

cat > "$CACHE_TMP" <<ENDJSON
{
  "timestamp": "$now",
  "repos": $repos_json,
  "totals": {
    "issues": $total_issues,
    "prs": $total_prs,
    "actionableIssues": $total_actionable_issues,
    "actionablePrs": $total_actionable_prs
  },
  "primary": {
    "stars": ${stars:-0},
    "forks": ${forks:-0},
    "contributors": ${contributors:-0},
    "adopters": ${adopters:-0},
    "acmm": ${acmm:-0}
  },
  "outreach": {
    "open": ${outreach_open:-0},
    "merged": ${outreach_merged:-0}
  }
}
ENDJSON

# Atomic move
mv "$CACHE_TMP" "$CACHE_FILE"

# Also write governor-compatible cache files for backward compat
gov_cache="${STATE_DIR:-/var/run/kick-governor}/repo_cache"
mkdir -p "$gov_cache" 2>/dev/null || true
for repo in "${REPOS[@]}"; do
  rname="${repo##*/}"
  araw=$(cat "$tmpdir/actionable_${rname}" 2>/dev/null || echo "0 0")
  ai="${araw%% *}"; [[ "$ai" == "-1" ]] && ai=0
  ap="${araw##* }"; [[ "$ap" == "-1" ]] && ap=0
  echo "$ai" > "$gov_cache/${rname}_actionable_issues"
  echo "$ap" > "$gov_cache/${rname}_actionable_prs"
done

echo "$total_actionable_issues" > "${STATE_DIR:-/var/run/kick-governor}/queue_issues"
echo "$total_actionable_prs" > "${STATE_DIR:-/var/run/kick-governor}/queue_prs"
