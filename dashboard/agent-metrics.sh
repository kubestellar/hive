#!/bin/bash

set +e
unset GITHUB_TOKEN

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
if command -v gh &>/dev/null; then
  # Open PRs
  open_prs=$(gh api 'repos/kubestellar/console/pulls?state=open&per_page=50' \
    --jq '[.[] | select(.user.login == "clubanderson") | {pr: .number, title: .title, body: (.body // ""), created: .created_at, state: "open"}]' 2>/dev/null || echo "[]")
  # Recently merged PRs (last 24h)
  since=$(date -u -d "-${RECENT_MERGED_HOURS} hours" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v-${RECENT_MERGED_HOURS}H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo "")
  merged_prs="[]"
  if [ -n "$since" ]; then
    merged_prs=$(gh api "repos/kubestellar/console/pulls?state=closed&per_page=30&sort=updated&direction=desc" \
      --jq "[.[] | select(.user.login == \"clubanderson\" and .merged_at != null and .merged_at >= \"$since\") | {pr: .number, title: .title, body: (.body // \"\"), merged: .merged_at, state: \"merged\"}]" 2>/dev/null || echo "[]")
  fi
  all_prs=$(echo "$open_prs" "$merged_prs" | jq -s 'add' 2>/dev/null || echo "[]")
  scanner_pairs_json=$(echo "$all_prs" | jq '[
    .[] |
    . as $p |
    ($p.body | match("(?i)(fixes|closes|resolves) #([0-9]+)"; "g") // null) as $m |
    if $m then { issue: ($m.captures[1].string | tonumber), pr: $p.pr, prTitle: $p.title, state: $p.state, created: ($p.created // null), merged: ($p.merged // null) } else empty end
  ]' 2>/dev/null || echo "[]")
  # Enrich with issue titles (parallel fetch)
  if [ "$scanner_pairs_json" != "[]" ]; then
    issue_tmp=$(mktemp -d)
    # Fetch all unique issue titles in parallel
    unique_issues=$(echo "$scanner_pairs_json" | jq -r '.[].issue' | sort -un | grep -v '^0$')
    for inum in $unique_issues; do
      (gh api "repos/kubestellar/console/issues/${inum}" --jq '.title' > "$issue_tmp/$inum" 2>/dev/null || echo "" > "$issue_tmp/$inum") &
    done
    wait
    # Build title lookup JSON
    title_map="{}"
    for inum in $unique_issues; do
      ititle=$(cat "$issue_tmp/$inum" 2>/dev/null || echo "")
      title_map=$(echo "$title_map" | jq --arg k "$inum" --arg v "$ititle" '. + {($k): $v}')
    done
    rm -rf "$issue_tmp"
    # Merge titles into pairs
    scanner_pairs_json=$(echo "$scanner_pairs_json" | jq --argjson titles "$title_map" '[.[] | .issueTitle = ($titles[(.issue|tostring)] // "")]')
  fi
fi

# Build agent JSON with live summaries and model
scanner_json=$(jq -n --arg doing "$scanner_doing" --arg model "$scanner_model" --argjson pairs "$scanner_pairs_json" '{doing: $doing, model: $model, pairs: $pairs}')
reviewer_json=$(jq -n --arg doing "$reviewer_doing" --arg model "$reviewer_model" '{doing: $doing, model: $model}')
architect_json=$(jq -n --arg doing "$architect_doing" --arg model "$architect_model" '{doing: $doing, model: $model}')
outreach_json=$(jq -n --arg doing "$outreach_doing" --arg model "$outreach_model" '{doing: $doing, model: $model}')

# ── Reviewer: coverage from README badge gist (authoritative source) ──
COVERAGE_BADGE_URL="https://gist.githubusercontent.com/clubanderson/b9a9ae8469f1897a22d5a40629bc1e82/raw/coverage-badge.json"
coverage_target=91
coverage_value=$(curl -sf "$COVERAGE_BADGE_URL" 2>/dev/null | jq -r '.message // "0"' | tr -d '%' || echo 0)
coverage_value=${coverage_value:-0}
reviewer_json=$(echo "$reviewer_json" | jq --argjson cv "$coverage_value" --argjson ct "$coverage_target" '. + {coverage: $cv, coverageTarget: $ct}')

# ── Outreach: growth, adoption, reach metrics (parallel) ──
outreach_tmp=$(mktemp -d)
(gh api repos/kubestellar/console --jq '.stargazers_count' > "$outreach_tmp/stars" 2>/dev/null || echo 0 > "$outreach_tmp/stars") &
(gh api repos/kubestellar/console --jq '.forks_count' > "$outreach_tmp/forks" 2>/dev/null || echo 0 > "$outreach_tmp/forks") &
(c=$(gh api repos/kubestellar/console/contributors?per_page=1 -i 2>/dev/null | grep -oP 'page=\K\d+(?=>; rel="last")' || echo 0); [ "$c" = "0" ] && c=$(gh api repos/kubestellar/console/contributors --jq 'length' 2>/dev/null || echo 0); echo "$c" > "$outreach_tmp/contribs") &
(a=$(gh api repos/kubestellar/console/contents/ADOPTERS.MD --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -cP '^\|.*\|.*\|' || echo 0); a=$(( a > 2 ? a - 2 : 0 )); echo "$a" > "$outreach_tmp/adopters") &
(unset GITHUB_TOKEN && gh api repos/kubestellar/docs/contents/src/app/%5Blocale%5D/acmm-leaderboard/page.tsx --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | sed -n '/BADGE_PARTICIPANTS = new Set/,/\]);/p' | grep -cP '^\s+"[a-zA-Z]' > "$outreach_tmp/acmm" 2>/dev/null || echo 0 > "$outreach_tmp/acmm") &
wait
stars=$(cat "$outreach_tmp/stars" 2>/dev/null || echo 0)
forks=$(cat "$outreach_tmp/forks" 2>/dev/null || echo 0)
contributors=$(cat "$outreach_tmp/contribs" 2>/dev/null || echo 0)
adopters_total=$(cat "$outreach_tmp/adopters" 2>/dev/null || echo 0)
acmm_count=$(cat "$outreach_tmp/acmm" 2>/dev/null || echo 0)
acmm_count=${acmm_count:-0}
rm -rf "$outreach_tmp"

# ── Architect: PR counts from tmux (live doing is summary) ──
architect_lines=$(tmux capture-pane -t feature -p -S -500 2>/dev/null)
architect_prs=$(echo "$architect_lines" | grep -oP 'pull/\d+' | sort -u | wc -l)
architect_prs=${architect_prs:-0}
architect_closed=$(echo "$architect_lines" | grep -ciP 'closed|resolved|stale')
architect_closed=${architect_closed:-0}

cat <<OUT
{
  "scanner": $scanner_json,
  "reviewer": $reviewer_json,
  "architect": $(jq -n --argjson json "$architect_json" --arg prs "$architect_prs" --arg closed "$architect_closed" '$json | .prs = ($prs|tonumber) | .closed = ($closed|tonumber)'),
  "outreach": $(jq -n --argjson json "$outreach_json" --argjson stars "$stars" --argjson forks "$forks" --argjson contribs "$contributors" --argjson adopters "$adopters_total" --argjson acmm "$acmm_count" '$json | .stars = $stars | .forks = $forks | .contributors = $contribs | .adopters = $adopters | .acmm = $acmm')
}
OUT
