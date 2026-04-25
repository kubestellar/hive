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
  # Enrich with issue titles
  if [ "$scanner_pairs_json" != "[]" ]; then
    enriched="[]"
    while IFS= read -r pair; do
      issue_num=$(echo "$pair" | jq -r '.issue')
      pr_num=$(echo "$pair" | jq -r '.pr')
      pr_title=$(echo "$pair" | jq -r '.prTitle')
      pr_state=$(echo "$pair" | jq -r '.state')
      pr_created=$(echo "$pair" | jq -r '.created // empty')
      pr_merged=$(echo "$pair" | jq -r '.merged // empty')
      issue_title=$(gh api "repos/kubestellar/console/issues/${issue_num}" --jq '.title' 2>/dev/null || echo "")
      enriched=$(echo "$enriched" | jq --argjson n "$issue_num" --argjson p "$pr_num" --arg pt "$pr_title" --arg it "$issue_title" --arg st "$pr_state" --arg cr "$pr_created" --arg mr "$pr_merged" \
        '. + [{issue: $n, pr: $p, prTitle: $pt, issueTitle: $it, state: $st, created: $cr, merged: $mr}]')
    done < <(echo "$scanner_pairs_json" | jq -c '.[]')
    scanner_pairs_json="$enriched"
  fi
fi

# Build agent JSON with live summaries and model
scanner_json=$(jq -n --arg doing "$scanner_doing" --arg model "$scanner_model" --argjson pairs "$scanner_pairs_json" '{doing: $doing, model: $model, pairs: $pairs}')
reviewer_json=$(jq -n --arg doing "$reviewer_doing" --arg model "$reviewer_model" '{doing: $doing, model: $model}')
architect_json=$(jq -n --arg doing "$architect_doing" --arg model "$architect_model" '{doing: $doing, model: $model}')
outreach_json=$(jq -n --arg doing "$outreach_doing" --arg model "$outreach_model" '{doing: $doing, model: $model}')

# ── Reviewer: coverage from reviewer.json ──
REVIEWER_METRICS_FILE="/var/run/hive-metrics/reviewer.json"
coverage_value=0
coverage_target=91
if [ -f "$REVIEWER_METRICS_FILE" ]; then
  coverage_value=$(jq -r '.coverage.value // 0' "$REVIEWER_METRICS_FILE" 2>/dev/null || echo 0)
  coverage_target=$(jq -r '.coverage.target // 91' "$REVIEWER_METRICS_FILE" 2>/dev/null || echo 91)
fi
reviewer_json=$(echo "$reviewer_json" | jq --argjson cv "$coverage_value" --argjson ct "$coverage_target" '. + {coverage: $cv, coverageTarget: $ct}')

# ── Outreach: growth, adoption, reach metrics ──
# Growth stats (REST API)
stars=$(gh api repos/kubestellar/console --jq '.stargazers_count' 2>/dev/null || echo 0)
forks=$(gh api repos/kubestellar/console --jq '.forks_count' 2>/dev/null || echo 0)
contributors=$(gh api repos/kubestellar/console/contributors?per_page=1 -i 2>/dev/null | grep -oP 'page=\K\d+(?=>; rel="last")' || echo 0)
[ "$contributors" = "0" ] && contributors=$(gh api repos/kubestellar/console/contributors --jq 'length' 2>/dev/null || echo 0)

# Adoption stats
adopters_total=$(gh api repos/kubestellar/console/contents/ADOPTERS.MD \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -cP '^\|.*\|.*\|' || echo 0)
adopters_total=$(( adopters_total > 2 ? adopters_total - 2 : 0 ))

# ACMM badges adopted
acmm_count=$(unset GITHUB_TOKEN && gh api repos/kubestellar/docs/contents/src/app/%5Blocale%5D/acmm-leaderboard/page.tsx \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null \
  | sed -n '/BADGE_PARTICIPANTS = new Set/,/\]);/p' \
  | grep -cP '^\s+"[a-zA-Z]' || echo 0)
acmm_count=${acmm_count:-0}

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
