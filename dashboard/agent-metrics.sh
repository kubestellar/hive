#!/bin/bash
# Per-agent metrics for dashboard — outputs JSON
# Uses REST API (not GraphQL) to avoid rate limit exhaustion
set +e
unset GITHUB_TOKEN

# ── Scanner: map issues → fix PRs from branch names ──
# REST API: get open PRs with fix/ prefix
scanner_json="[]"
fix_prs=$(gh api "repos/kubestellar/console/pulls?state=open&per_page=100" \
  --jq '.[] | select(.head.ref | startswith("fix/")) | "\(.number) \(.head.ref)"' 2>/dev/null || echo "")
if [ -n "$fix_prs" ]; then
  scanner_json=$(echo "$fix_prs" | while read -r pr branch; do
    issue=$(echo "$branch" | grep -oP '\d+$' || echo "")
    [ -n "$issue" ] && echo "{\"issue\":$issue,\"pr\":$pr}"
  done | jq -s '.')
fi

# ── Reviewer: health checks come from health-check.sh separately ──

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
marketplace=$(gh api repos/kubestellar/console-marketplace/contents/registry.json \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | jq 'length' 2>/dev/null || echo 0)
brew_formulas=$(gh api repos/kubestellar/homebrew-tap/contents/Formula --jq 'length' 2>/dev/null || echo 0)

# Read agent-authored summary
outreach_summary=$(cat /var/run/hive-metrics/outreach_summary.txt 2>/dev/null || echo "")
outreach_summary=${outreach_summary:-"no summary yet"}
outreach_summary_json=$(echo "$outreach_summary" | head -1 | head -c 120 | jq -Rs '.')

# ── Architect: exec summary from status file + PR/proposal counts ──
architect_lines=$(tmux capture-pane -t feature -p -S -500 2>/dev/null)
architect_prs=$(echo "$architect_lines" | grep -oP 'pull/\d+' | sort -u | wc -l)
architect_prs=${architect_prs:-0}
architect_closed=$(echo "$architect_lines" | grep -ciP 'closed|resolved|stale')
architect_closed=${architect_closed:-0}
# Read agent-authored summary (agents write this themselves each pass)
architect_summary=$(cat /var/run/hive-metrics/architect_summary.txt 2>/dev/null || echo "")
architect_summary=${architect_summary:-"no summary yet"}
architect_summary_json=$(echo "$architect_summary" | head -1 | head -c 120 | jq -Rs '.')

cat <<EOF
{
  "scanner": {"pairs": $scanner_json},
  "reviewer": {},
  "outreach": {"stars": ${stars:-0}, "forks": ${forks:-0}, "contributors": ${contributors:-0}, "adopters": $adopters_total, "marketplace": ${marketplace:-0}, "brewFormulas": ${brew_formulas:-0}, "summary": $outreach_summary_json},
  "architect": {"prs": $architect_prs, "closed": $architect_closed, "summary": $architect_summary_json}
}
EOF
