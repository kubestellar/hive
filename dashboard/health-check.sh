#!/bin/bash
# Health checks for hive dashboard — outputs JSON
# Reads workflow names and repo from hive-project.yaml via hive-config.sh.
# Falls back to hardcoded kubestellar defaults if no config file exists.
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

# Fallback defaults (kubestellar) when no config loaded
REPO="${PROJECT_PRIMARY_REPO:-kubestellar/console}"
BREW_TAP="${HEALTH_BREW_TAP_REPO:-kubestellar/homebrew-tap}"
BREW_FORMULA="${HEALTH_BREW_FORMULA:-kubestellar-console.rb}"
HELM_PATH="${HEALTH_HELM_CHART_PATH:-deploy/helm/kubestellar-console/Chart.yaml}"
CI_WF="${HEALTH_CI_WORKFLOW:-Build and Deploy KC}"
REL_WF="${HEALTH_RELEASE_WORKFLOW:-Release}"

# ── CI pass rate (last 10 completed runs) ────────────────────────────────────
ci=$(gh run list --repo "$REPO" --limit 10 --json conclusion,status \
  --jq '[.[] | select(.status=="completed")] | if length > 0 then ([.[] | select(.conclusion=="success" or .conclusion=="skipped")] | length) * 100 / length | floor else 0 end' 2>/dev/null || echo 0)

# ── Brew formula freshness ───────────────────────────────────────────────────
if [ -n "$BREW_TAP" ] && [ -n "$BREW_FORMULA" ]; then
  formula_ver=$(gh api "repos/${BREW_TAP}/contents/Formula/${BREW_FORMULA}" \
    --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep -oP 'version "\K[^"]+' | sed 's/^v//' || echo "?")
  latest_rel=$(gh api "repos/${REPO}/releases/latest" --jq '.tag_name' 2>/dev/null | sed 's/^v//' || echo "?")
  brew_ok=$( [ "$formula_ver" = "$latest_rel" ] && echo 1 || echo 0 )
else
  brew_ok=-1
fi

# ── Helm chart exists ────────────────────────────────────────────────────────
if [ -n "$HELM_PATH" ]; then
  helm_ok=$(gh api "repos/${REPO}/contents/${HELM_PATH}" --jq '.name' >/dev/null 2>&1 && echo 1 || echo 0)
else
  helm_ok=-1
fi

# ── Workflow checks (config-driven) ─────────────────────────────────────────
check_workflow() {
  local result
  result=$(gh run list --repo "$REPO" --workflow "$1" --limit 1 \
    --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
  [ "$result" = "success" ] && echo 1 || echo 0
}

# Build JSON object from configured workflows
workflow_json=""
if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
  workflow_count=$(python3 -c "import json; wfs=json.loads('${HEALTH_CHECK_WORKFLOWS}'); print(len(wfs))" 2>/dev/null || echo 0)
  if [ "$workflow_count" -gt 0 ]; then
    for i in $(seq 0 $((workflow_count - 1))); do
      wf_name=$(python3 -c "import json; print(json.loads('${HEALTH_CHECK_WORKFLOWS}')[$i]['name'])" 2>/dev/null)
      wf_key=$(python3 -c "import json; wf=json.loads('${HEALTH_CHECK_WORKFLOWS}')[$i]; print(wf.get('key', wf['name'].lower().replace(' ','_').replace('-','_')))" 2>/dev/null)
      result=$(check_workflow "$wf_name")
      workflow_json="${workflow_json}\"${wf_key}\":${result},"
    done
  fi
else
  # Fallback: hardcoded kubestellar nightly checks
  workflow_json="\"nightly\":$(check_workflow "Nightly Test Suite"),"
  workflow_json="${workflow_json}\"nightlyCompliance\":$(check_workflow "Nightly Compliance & Perf"),"
  workflow_json="${workflow_json}\"nightlyDashboard\":$(check_workflow "Nightly Dashboard Health"),"
  workflow_json="${workflow_json}\"nightlyGhaw\":$(check_workflow "Nightly gh-aw Version Check"),"
  workflow_json="${workflow_json}\"nightlyPlaywright\":$(check_workflow "Playwright Cross-Browser (Nightly)"),"
fi

# ── Release workflow — nightly and weekly ────────────────────────────────────
if [ -n "$REL_WF" ]; then
  nightly_rel=$(gh run list --repo "$REPO" --workflow "$REL_WF" --event schedule --limit 10 \
    --json conclusion,createdAt --jq '[.[] | select((.createdAt | strptime("%Y-%m-%dT%H:%M:%SZ") | strftime("%u")) != "7")][0].conclusion // "none"' 2>/dev/null || echo "none")
  nightly_rel_ok=$( [ "$nightly_rel" = "success" ] && echo 1 || echo 0 )
  weekly_rel=$(gh run list --repo "$REPO" --workflow "$REL_WF" --event schedule --limit 10 \
    --json conclusion,createdAt --jq '[.[] | select((.createdAt | strptime("%Y-%m-%dT%H:%M:%SZ") | strftime("%u")) == "7")][0].conclusion // "none"' 2>/dev/null || echo "none")
  weekly_rel_ok=$( [ "$weekly_rel" = "success" ] && echo 1 || echo 0 )
else
  nightly_rel_ok=-1
  weekly_rel_ok=-1
fi

# ── Weekly workflow ──────────────────────────────────────────────────────────
# Check for a "Weekly" type workflow from config, or fall back to hardcoded
weekly_ok=-1
if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
  weekly_wf=$(python3 -c "
import json
wfs = json.loads('${HEALTH_CHECK_WORKFLOWS}')
weekly = [w for w in wfs if w.get('type') == 'weekly']
print(weekly[0]['name'] if weekly else '')
" 2>/dev/null)
  if [ -n "$weekly_wf" ]; then
    weekly=$(gh run list --repo "$REPO" --workflow "$weekly_wf" --limit 1 \
      --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
    weekly_ok=$( [ "$weekly" = "success" ] && echo 1 || echo 0 )
  fi
else
  weekly=$(gh run list --repo "$REPO" --workflow "Weekly Coverage Review" --limit 1 \
    --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
  weekly_ok=$( [ "$weekly" = "success" ] && echo 1 || echo 0 )
fi

# ── Perf workflows — worst of all ───────────────────────────────────────────
hourly_worst=1
if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
  perf_wfs=$(python3 -c "import json; [print(w) for w in json.loads('${HEALTH_PERF_WORKFLOWS}')]" 2>/dev/null)
  if [ -n "$perf_wfs" ]; then
    while IFS= read -r wf; do
      result=$(gh run list --repo "$REPO" --workflow "$wf" --limit 1 \
        --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
      if [ "$result" = "failure" ]; then hourly_worst=0; fi
    done <<< "$perf_wfs"
  fi
else
  for wf in "Perf — React commits per navigation" "Performance TTFI Gate"; do
    result=$(gh run list --repo "$REPO" --workflow "$wf" --limit 1 \
      --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
    if [ "$result" = "failure" ]; then hourly_worst=0; fi
  done
fi
hourly_ok=$hourly_worst

# ── Deploy checks ────────────────────────────────────────────────────────────
deploy_json=""
if [ -n "$CI_WF" ]; then
  deploy_run_id=$(gh run list --repo "$REPO" --workflow "$CI_WF" \
    --event push --branch main --limit 1 \
    --json databaseId --jq '.[0].databaseId' 2>/dev/null || echo "")

  if [ -n "$deploy_run_id" ]; then
    if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
      job_count=$(python3 -c "import json; print(len(json.loads('${HEALTH_DEPLOY_JOBS}')))" 2>/dev/null || echo 0)
      if [ "$job_count" -gt 0 ]; then
        for i in $(seq 0 $((job_count - 1))); do
          job_name=$(python3 -c "import json; print(json.loads('${HEALTH_DEPLOY_JOBS}')[$i]['name'])" 2>/dev/null)
          job_key=$(echo "$job_name" | tr '-' '_')
          result=$(gh run view "$deploy_run_id" --repo "$REPO" --json jobs \
            --jq ".jobs[] | select(.name == \"${job_name}\") | .conclusion" 2>/dev/null || echo "none")
          ok=$( [ "$result" = "success" ] && echo 1 || echo 0 )
          deploy_json="${deploy_json}\"${job_key}\":${ok},"
        done
      fi
    else
      # Fallback: hardcoded kubestellar deploy jobs
      vllm_result=$(gh run view "$deploy_run_id" --repo "$REPO" --json jobs \
        --jq '.jobs[] | select(.name == "deploy-vllm-d") | .conclusion' 2>/dev/null || echo "none")
      pokprod_result=$(gh run view "$deploy_run_id" --repo "$REPO" --json jobs \
        --jq '.jobs[] | select(.name == "deploy-pok-prod") | .conclusion' 2>/dev/null || echo "none")
      deploy_json="\"vllm\":$( [ "$vllm_result" = "success" ] && echo 1 || echo 0 ),"
      deploy_json="${deploy_json}\"pokprod\":$( [ "$pokprod_result" = "success" ] && echo 1 || echo 0 ),"
    fi
  fi
fi

# ── Output JSON ──────────────────────────────────────────────────────────────
# Remove trailing comma from accumulated JSON fragments
workflow_json="${workflow_json%,}"
deploy_json="${deploy_json%,}"

cat <<EOF
{"ci":${ci:-0},"brew":$brew_ok,"helm":$helm_ok,${workflow_json:+${workflow_json},}"nightlyRel":$nightly_rel_ok,"weekly":$weekly_ok,"weeklyRel":$weekly_rel_ok,"hourly":$hourly_ok${deploy_json:+,${deploy_json}}}
EOF
