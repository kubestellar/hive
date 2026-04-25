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
latest_rel=$(gh release list --repo kubestellar/console --limit 1 --exclude-pre-releases \
  --json tagName --jq '.[0].tagName' 2>/dev/null | sed 's/^v//' || echo "?")
brew_ok=$( [ "$formula_ver" = "$latest_rel" ] && echo 1 || echo 0 )

# Helm chart exists
helm_ok=$(gh api repos/kubestellar/console/contents/deploy/helm/kubestellar-console/Chart.yaml --jq '.name' >/dev/null 2>&1 && echo 1 || echo 0)

# Nightly workflow
nightly=$(gh run list --repo kubestellar/console --workflow "Nightly Test Suite" --limit 1 \
  --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
nightly_ok=$( [ "$nightly" = "success" ] && echo 1 || echo 0 )

# Weekly workflow
weekly=$(gh run list --repo kubestellar/console --workflow "Weekly Coverage Review" --limit 1 \
  --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
weekly_ok=$( [ "$weekly" = "success" ] && echo 1 || echo 0 )

# Deploy checks (placeholder — no endpoints configured yet)
vllm_ok=-1
pokprod_ok=-1

cat <<EOF
{"ci":${ci:-0},"brew":$brew_ok,"helm":$helm_ok,"nightly":$nightly_ok,"weekly":$weekly_ok,"vllm":$vllm_ok,"pokprod":$pokprod_ok}
EOF
