#!/bin/bash
# Agent metrics for hive dashboard — reads project config for repo/author.

set +e
unset GITHUB_TOKEN
[ -n "$HIVE_GITHUB_TOKEN" ] && export GH_TOKEN="$HIVE_GITHUB_TOKEN"

# Load project config
SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
if [ -f /usr/local/bin/hive-config.sh ]; then
  source /usr/local/bin/hive-config.sh
elif [ -f "$SCRIPT_DIR/bin/hive-config.sh" ]; then
  source "$SCRIPT_DIR/bin/hive-config.sh"
fi

# Use real gh binary — the /usr/local/bin/gh wrapper blocks listing commands
# (designed for agents) which would break this infrastructure script.
GH=/usr/bin/gh

REPO="${PROJECT_PRIMARY_REPO:-kubestellar/console}"
AI_AUTHOR="${PROJECT_AI_AUTHOR:-${AI_AUTHOR}}"
PROJECT="${PROJECT_NAME:-KubeStellar}"
BADGE_URL="${OUTREACH_COVERAGE_BADGE_URL:-https://gist.githubusercontent.com/${AI_AUTHOR}/b9a9ae8469f1897a22d5a40629bc1e82/raw/coverage-badge.json}"

# Get live agent status (includes doing field with live spinner updates)
agent_status=$(hive status --json 2>/dev/null)

# Extract live summary/doing for each agent
scanner_doing=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "scanner") | .doing' 2>/dev/null || echo "")
scanner_model=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "scanner") | .model' 2>/dev/null || echo "?")
reviewer_doing=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "reviewer") | .doing' 2>/dev/null || echo "")
reviewer_model=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "reviewer") | .model' 2>/dev/null || echo "?")
architect_doing=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "architect") | .doing' 2>/dev/null || echo "")
architect_model=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "architect") | .model' 2>/dev/null || echo "?")
outreach_doing=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "outreach") | .doing' 2>/dev/null || echo "")
outreach_model=$(echo "$agent_status" | jq -r '.agents[] | select(.name == "outreach") | .model' 2>/dev/null || echo "?")

# ── Scanner: issue→PR pairs from open + recently merged AI-authored PRs ──
RECENT_MERGED_HOURS=24
scanner_pairs_json="[]"
if command -v $GH &>/dev/null; then
  # Open PRs
  open_prs=$($GH api "repos/${REPO}/pulls?state=open&per_page=50" \
    --jq "[.[] | select(.user.login == \"${AI_AUTHOR}\") | {pr: .number, title: .title, body: (.body // \"\"), created: .created_at, state: \"open\"}]" 2>/dev/null || echo "[]")
  # Recently merged PRs (last 24h)
  since=$(date -u -d "-${RECENT_MERGED_HOURS} hours" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v-${RECENT_MERGED_HOURS}H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo "")
  merged_prs="[]"
  if [ -n "$since" ]; then
    merged_prs=$($GH api "repos/${REPO}/pulls?state=closed&per_page=30&sort=updated&direction=desc" \
      --jq "[.[] | select(.user.login == \"${AI_AUTHOR}\" and .merged_at != null and .merged_at >= \"$since\") | {pr: .number, title: .title, body: (.body // \"\"), merged: .merged_at, state: \"merged\"}]" 2>/dev/null || echo "[]")
  fi
  all_prs=$(echo "$open_prs" "$merged_prs" | jq -s 'add' 2>/dev/null || echo "[]")
  scanner_pairs_json=$(echo "$all_prs" | jq '[
    .[] |
    . as $p |
    ($p.body | match("(?i)(fixes|closes|resolves) #([0-9]+)"; "g") // null) as $m |
    if $m then { issue: ($m.captures[1].string | tonumber), pr: $p.pr, prTitle: $p.title, state: $p.state, created: ($p.created // null), merged: ($p.merged // null) } else empty end
  ]' 2>/dev/null || echo "[]")
  # ── In-progress issues: mentioned in scanner tmux but no PR yet ──
  scanner_tmux=$(tmux capture-pane -t scanner -p -S -200 2>/dev/null || echo "")
  dispatched_issues=$(echo "$scanner_tmux" | grep -oP '#\K\d{4,5}' | sort -un)
  pr_issues=$(echo "$scanner_pairs_json" | jq -r '.[].issue' 2>/dev/null | sort -un)
  in_progress_issues=$(comm -23 <(echo "$dispatched_issues") <(echo "$pr_issues") 2>/dev/null | head -10)
  scanner_inprogress_json="[]"
  if [ -n "$in_progress_issues" ]; then
    ip_tmp=$(mktemp -d)
    for inum in $in_progress_issues; do
      ($GH api "repos/${REPO}/issues/${inum}" --jq '{number: .number, title: .title, state: .state, labels: [.labels[].name]}' > "$ip_tmp/$inum" 2>/dev/null || echo "{\"number\":$inum,\"title\":\"\",\"state\":\"open\",\"labels\":[]}" > "$ip_tmp/$inum") &
    done
    wait
    scanner_inprogress_json=$(for inum in $in_progress_issues; do cat "$ip_tmp/$inum" 2>/dev/null; done | jq -s '[.[] | select(.state == "open" and ([.labels[] | select(. == "hold")] | length == 0))]' 2>/dev/null || echo "[]")
    rm -rf "$ip_tmp"
  fi

  # Enrich with issue titles — cached to avoid redundant GH API calls.
  # Merged-pair titles are immutable; open-pair titles refresh every cycle.
  TITLE_CACHE_FILE="/var/run/hive-metrics/issue-title-cache.json"
  if [ "$scanner_pairs_json" != "[]" ]; then
    title_cache=$(cat "$TITLE_CACHE_FILE" 2>/dev/null || echo '{}')
    # Validate JSON — corrupt cache shouldn't break the pipeline
    echo "$title_cache" | jq . >/dev/null 2>&1 || title_cache='{}'

    unique_issues=$(echo "$scanner_pairs_json" | jq -r '.[].issue' | sort -un | grep -v '^0$')
    merged_issues=$(echo "$scanner_pairs_json" | jq -r '.[] | select(.state == "merged") | .issue' | sort -un | grep -v '^0$')

    # Determine which issues need a fetch: skip merged issues already in cache
    fetch_issues=""
    for inum in $unique_issues; do
      cached_title=$(echo "$title_cache" | jq -r --arg k "$inum" '.[$k].title // empty' 2>/dev/null)
      is_merged=$(echo "$merged_issues" | grep -qx "$inum" && echo "yes" || echo "no")
      if [ "$is_merged" = "yes" ] && [ -n "$cached_title" ]; then
        continue
      fi
      fetch_issues="$fetch_issues $inum"
    done

    # Fetch only uncached issues in parallel
    title_map="{}"
    state_map="{}"
    if [ -n "$fetch_issues" ]; then
      issue_tmp=$(mktemp -d)
      for inum in $fetch_issues; do
        ($GH api "repos/${REPO}/issues/${inum}" --jq '{title: .title, state: .state}' > "$issue_tmp/$inum" 2>/dev/null || echo '{"title":"","state":"open"}' > "$issue_tmp/$inum") &
      done
      wait
      for inum in $fetch_issues; do
        ititle=$(cat "$issue_tmp/$inum" 2>/dev/null | jq -r '.title // ""')
        istate=$(cat "$issue_tmp/$inum" 2>/dev/null | jq -r '.state // "open"')
        title_map=$(echo "$title_map" | jq --arg k "$inum" --arg v "$ititle" '. + {($k): $v}')
        state_map=$(echo "$state_map" | jq --arg k "$inum" --arg v "$istate" '. + {($k): $v}')
        # Write to cache (merged titles persist forever; open titles update each cycle)
        title_cache=$(echo "$title_cache" | jq --arg k "$inum" --arg t "$ititle" --arg s "$istate" '. + {($k): {title: $t, state: $s}}')
      done
      rm -rf "$issue_tmp"
    fi

    # Fill title_map and state_map from cache for issues we didn't fetch
    for inum in $unique_issues; do
      existing=$(echo "$title_map" | jq -r --arg k "$inum" '.[$k] // empty' 2>/dev/null)
      if [ -z "$existing" ]; then
        ctitle=$(echo "$title_cache" | jq -r --arg k "$inum" '.[$k].title // ""' 2>/dev/null)
        cstate=$(echo "$title_cache" | jq -r --arg k "$inum" '.[$k].state // "open"' 2>/dev/null)
        title_map=$(echo "$title_map" | jq --arg k "$inum" --arg v "$ctitle" '. + {($k): $v}')
        state_map=$(echo "$state_map" | jq --arg k "$inum" --arg v "$cstate" '. + {($k): $v}')
      fi
    done

    # Persist cache
    echo "$title_cache" > "$TITLE_CACHE_FILE" 2>/dev/null || true

    # Merge titles into pairs; drop open PRs where the issue is already closed
    scanner_pairs_json=$(echo "$scanner_pairs_json" | jq --argjson titles "$title_map" --argjson states "$state_map" '[.[] | .issueTitle = ($titles[(.issue|tostring)] // "") | select(.state == "merged" or ($states[(.issue|tostring)] // "open") != "closed")]')
  fi
fi

# Build agent JSON with live summaries and model
scanner_json=$(jq -n --arg doing "$scanner_doing" --arg model "$scanner_model" --argjson pairs "$scanner_pairs_json" --argjson inProgress "$scanner_inprogress_json" '{doing: $doing, model: $model, pairs: $pairs, inProgress: $inProgress}')
reviewer_json=$(jq -n --arg doing "$reviewer_doing" --arg model "$reviewer_model" '{doing: $doing, model: $model}')
architect_json=$(jq -n --arg doing "$architect_doing" --arg model "$architect_model" '{doing: $doing, model: $model}')
outreach_json=$(jq -n --arg doing "$outreach_doing" --arg model "$outreach_model" '{doing: $doing, model: $model}')

# ── Reviewer: coverage from README badge gist (authoritative source) ──
COVERAGE_BADGE_URL="$BADGE_URL"
coverage_target=91
coverage_value=$(curl -sf "$COVERAGE_BADGE_URL" 2>/dev/null | jq -r '.message // "0"' | tr -d '%' || echo 0)
coverage_value=${coverage_value:-0}
reviewer_json=$(echo "$reviewer_json" | jq --argjson cv "$coverage_value" --argjson ct "$coverage_target" '. + {coverage: $cv, coverageTarget: $ct}')

# ── Outreach: read from centralized api-collector cache (no extra API calls) ──
GITHUB_CACHE="${HIVE_METRICS_DIR:-/var/run/hive-metrics}/github-cache.json"
if [ -f "$GITHUB_CACHE" ]; then
  stars=$(jq -r '.primary.stars // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
  forks=$(jq -r '.primary.forks // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
  contributors=$(jq -r '.primary.contributors // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
  adopters_total=$(jq -r '.primary.adopters // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
  acmm_count=$(jq -r '.primary.acmm // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
  outreach_open=$(jq -r '.outreach.open // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
  outreach_merged=$(jq -r '.outreach.merged // 0' "$GITHUB_CACHE" 2>/dev/null || echo 0)
else
  stars=0; forks=0; contributors=0; adopters_total=0; acmm_count=0; outreach_open=0; outreach_merged=0
fi

# ── Architect: PR counts from tmux (live doing is summary) ──
architect_lines=$(tmux capture-pane -t architect -p -S -500 2>/dev/null)
architect_prs=$(echo "$architect_lines" | grep -oP 'pull/\d+' | sort -u | wc -l)
architect_prs=${architect_prs:-0}
architect_closed=$(echo "$architect_lines" | grep -ciP 'closed|resolved|stale')
architect_closed=${architect_closed:-0}

cat <<OUT
{
  "scanner": $scanner_json,
  "reviewer": $reviewer_json,
  "architect": $(jq -n --argjson json "$architect_json" --arg prs "$architect_prs" --arg closed "$architect_closed" '$json | .prs = ($prs|tonumber) | .closed = ($closed|tonumber)'),
  "outreach": $(jq -n --argjson json "$outreach_json" --argjson stars "$stars" --argjson forks "$forks" --argjson contribs "$contributors" --argjson adopters "$adopters_total" --argjson acmm "$acmm_count" --argjson orOpen "$outreach_open" --argjson orMerged "$outreach_merged" '$json | .stars = $stars | .forks = $forks | .contributors = $contribs | .adopters = $adopters | .acmm = $acmm | .outreachOpen = $orOpen | .outreachMerged = $orMerged')
}
OUT
