#!/bin/bash
# Health checks for hive dashboard — outputs JSON
set +e
unset GITHUB_TOKEN

# CI pass rate (last 10 completed runs)
ci=$(gh run list --repo kubestellar/console --limit 10 --json conclusion,status \
  --jq '[.[] | select(.status=="completed")] | if length > 0 then ([.[] | select(.conclusion=="success" or .conclusion=="skipped")] | length) * 100 / length | floor else 0 end' 2>/dev/null || echo 0)

# Brew formula freshness
formula_ver=$(gh api repos/kubestellar/homebrew-tap/contents/Formula/kubestellar-console.rb \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -oP 'version "\K[^"]+' | sed 's/^v//' || echo "?")
latest_rel=$(gh api repos/kubestellar/console/releases/latest --jq '.tag_name' 2>/dev/null | sed 's/^v//' || echo "?")
brew_ok=$( [ "$formula_ver" = "$latest_rel" ] && echo 1 || echo 0 )

# Helm chart exists
helm_ok=$(gh api repos/kubestellar/console/contents/deploy/helm/kubestellar-console/Chart.yaml --jq '.name' >/dev/null 2>&1 && echo 1 || echo 0)

# Nightly workflows — check each individually
check_workflow() {
  local result
  result=$(gh run list --repo kubestellar/console --workflow "$1" --limit 1 \
    --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
  [ "$result" = "success" ] && echo 1 || echo 0
}

nightly_ok=$(check_workflow "Nightly Test Suite")
nightly_compliance_ok=$(check_workflow "Nightly Compliance & Perf")
nightly_dashboard_ok=$(check_workflow "Nightly Dashboard Health")
nightly_ghaw_ok=$(check_workflow "Nightly gh-aw Version Check")
nightly_playwright_ok=$(check_workflow "Playwright Cross-Browser (Nightly)")

# Release workflow — runs nightly AND weekly (Sunday), same workflow different cron
# Nightly release: last non-Sunday scheduled run
nightly_rel=$(gh run list --repo kubestellar/console --workflow "Release" --event schedule --limit 10 \
  --json conclusion,createdAt --jq '[.[] | select((.createdAt | strptime("%Y-%m-%dT%H:%M:%SZ") | strftime("%u")) != "7")][0].conclusion // "none"' 2>/dev/null || echo "none")
nightly_rel_ok=$( [ "$nightly_rel" = "success" ] && echo 1 || echo 0 )
# Weekly release: last Sunday scheduled run
weekly_rel=$(gh run list --repo kubestellar/console --workflow "Release" --event schedule --limit 10 \
  --json conclusion,createdAt --jq '[.[] | select((.createdAt | strptime("%Y-%m-%dT%H:%M:%SZ") | strftime("%u")) == "7")][0].conclusion // "none"' 2>/dev/null || echo "none")
weekly_rel_ok=$( [ "$weekly_rel" = "success" ] && echo 1 || echo 0 )

# Weekly workflow
weekly=$(gh run list --repo kubestellar/console --workflow "Weekly Coverage Review" --limit 1 \
  --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
weekly_ok=$( [ "$weekly" = "success" ] && echo 1 || echo 0 )

# Hourly/Perf workflows — worst of all perf workflows
hourly_worst=1
for wf in "Perf — React commits per navigation" "Performance TTFI Gate"; do
  result=$(gh run list --repo kubestellar/console --workflow "$wf" --limit 1 \
    --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
  if [ "$result" = "failure" ]; then hourly_worst=0; fi
done
hourly_ok=$hourly_worst

# Deploy checks (placeholder — no endpoints configured yet)
vllm_ok=-1
pokprod_ok=-1

cat <<EOF
{"ci":${ci:-0},"brew":$brew_ok,"helm":$helm_ok,"nightly":$nightly_ok,"nightlyCompliance":$nightly_compliance_ok,"nightlyDashboard":$nightly_dashboard_ok,"nightlyGhaw":$nightly_ghaw_ok,"nightlyPlaywright":$nightly_playwright_ok,"nightlyRel":$nightly_rel_ok,"weekly":$weekly_ok,"weeklyRel":$weekly_rel_ok,"hourly":$hourly_ok,"vllm":$vllm_ok,"pokprod":$pokprod_ok}
EOF
