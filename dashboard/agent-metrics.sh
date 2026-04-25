#!/bin/bash
# Per-agent metrics for dashboard — outputs JSON
set +e
unset GITHUB_TOKEN

# ── Scanner: map issues → fix PRs from branch names ──
# Get fix/ PRs with branch names to extract linked issue numbers
scanner_pairs=$(unset GITHUB_TOKEN && gh pr list --repo kubestellar/console --state open \
  --json number,headRefName --jq '.[] | select(.headRefName | startswith("fix/")) | "\(.number) \(.headRefName)"' 2>/dev/null || echo "")
scanner_json="[]"
if [ -n "$scanner_pairs" ]; then
  # Build array of {issue, pr} objects from branch name pattern fix/*-NNNN
  scanner_json=$(echo "$scanner_pairs" | while read -r pr branch; do
    issue=$(echo "$branch" | grep -oP '\d+$' || echo "")
    [ -n "$issue" ] && echo "{\"issue\":$issue,\"pr\":$pr}"
  done | jq -s '.')
fi

# ── Reviewer: health checks come from health-check.sh separately ──
# Reviewer shows: CI pass rate, brew, helm, nightly, weekly, vllm-d, pokprod
# These are already in healthChecks — just flag that reviewer owns them

# ── Outreach: GA4 errors, adopter PRs, adoption stats ──
# GA4 open error issues
ga4_errors=$(unset GITHUB_TOKEN && gh issue list --repo kubestellar/console --state open \
  --label "ga4-error" --json number --jq 'length' 2>/dev/null || echo 0)
ga4_errors=${ga4_errors:-0}
# Open adopter/outreach PRs
adopter_prs=$(unset GITHUB_TOKEN && gh pr list --repo kubestellar/console --state open \
  --json number,headRefName --jq '[.[] | select(.headRefName | test("outreach|adopter")) | .number]' 2>/dev/null || echo "[]")
adopter_prs=${adopter_prs:-"[]"}
adopter_count=$(echo "$adopter_prs" | jq 'length' 2>/dev/null || echo 0)
# Read agent-authored summary
outreach_summary=$(cat /var/run/hive-metrics/outreach_summary.txt 2>/dev/null || echo "")
outreach_summary=${outreach_summary:-"no summary yet"}
outreach_summary_json=$(echo "$outreach_summary" | head -1 | head -c 120 | jq -Rs '.')
# Count current adopters in ADOPTERS.MD (merged lines)
adopters_total=$(unset GITHUB_TOKEN && gh api repos/kubestellar/console/contents/ADOPTERS.MD \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -cP '^\|.*\|.*\|' || echo 0)
# Subtract header rows (2)
adopters_total=$(( adopters_total > 2 ? adopters_total - 2 : 0 ))

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
  "outreach": {"ga4Errors": $ga4_errors, "adopterPrs": $adopter_prs, "adopterPending": $adopter_count, "adoptersTotal": $adopters_total, "summary": $outreach_summary_json},
  "architect": {"prs": $architect_prs, "closed": $architect_closed, "summary": $architect_summary_json}
}
EOF
