#!/bin/bash
# Health checks for hive dashboard — outputs JSON
# Reads workflow names and repo from hive-project.yaml via hive-config.sh.
# Falls back to hardcoded kubestellar defaults if no config file exists.
set +e
unset GITHUB_TOKEN

# Use hive GitHub App token — never fall back to personal gh auth
GH_APP_TOKEN_CACHE="/var/run/hive-metrics/gh-app-token.cache"
if [[ -f "$GH_APP_TOKEN_CACHE" ]]; then
  export GH_TOKEN="$(cat "$GH_APP_TOKEN_CACHE")"
elif [[ -n "${HIVE_GITHUB_TOKEN:-}" ]]; then
  export GH_TOKEN="$HIVE_GITHUB_TOKEN"
else
  echo '{"error":"no hive app token available"}' >&2
  echo '{"ci":0,"brew":-1,"helm":-1,"nightlyRel":-1,"weeklyRel":-1,"weekly":-1,"hourly":-1}'
  exit 0
fi

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
BREW_FORMULA="${HEALTH_BREW_FORMULA:-kc-agent.rb}"
HELM_PATH="${HEALTH_HELM_CHART_PATH:-deploy/helm/kubestellar-console/Chart.yaml}"
CI_WF="${HEALTH_CI_WORKFLOW:-Build and Deploy KC}"
REL_WF="${HEALTH_RELEASE_WORKFLOW:-Release}"

# ── CI pass rate (last 10 completed runs) ────────────────────────────────────
ci=$(gh run list --repo "$REPO" --status completed --limit 10 --json conclusion \
  --jq 'if length > 0 then ([.[] | select(.conclusion=="success" or .conclusion=="skipped")] | length) * 100 / length | floor else 0 end' 2>/dev/null || echo 0)

# ── Brew formula freshness ───────────────────────────────────────────────────
# Nightly releases set the formula to e.g. "0.3.26-nightly.20260505".
# Green when: nightly suffix matches today's date, OR stable version matches latest release.
if [ -n "$BREW_TAP" ] && [ -n "$BREW_FORMULA" ]; then
  formula_ver=$(gh api "repos/${BREW_TAP}/contents/Formula/${BREW_FORMULA}" \
    --jq '.content' 2>/dev/null | base64 -d 2>/dev/null | grep 'version "' | sed 's/.*version "//;s/".*//' | sed 's/^v//' || echo "?")
  today=$(TZ=America/New_York date +%Y%m%d)
  if echo "$formula_ver" | grep -q "nightly\.${today}$"; then
    brew_ok=1
  else
    latest_rel=$(gh api "repos/${REPO}/releases/latest" --jq '.tag_name' 2>/dev/null | sed 's/^v//' || echo "?")
    brew_ok=$( [ "$formula_ver" = "$latest_rel" ] && echo 1 || echo 0 )
  fi
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
  result=$(gh run list --repo "$REPO" --workflow "$1" --status completed --limit 1 \
    --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
  [ "$result" = "success" ] && echo 1 || echo 0
}

# Build JSON object from configured workflows
workflow_json=""
if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
  workflow_count=$(echo "${HEALTH_CHECK_WORKFLOWS}" | python3 -c "import sys,json; wfs=json.loads(sys.stdin.read()); print(len(wfs))" 2>/dev/null || echo 0)
  if [ "$workflow_count" -gt 0 ]; then
    for i in $(seq 0 $((workflow_count - 1))); do
      wf_name=$(echo "${HEALTH_CHECK_WORKFLOWS}" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())[$i]['name'])" 2>/dev/null)
      wf_key=$(echo "${HEALTH_CHECK_WORKFLOWS}" | python3 -c "import sys,json; wf=json.loads(sys.stdin.read())[$i]; print(wf.get('key', wf['name'].lower().replace(' ','_').replace('-','_')))" 2>/dev/null)
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

# ── Release freshness — nightly (24h) and weekly (7d) ────────────────────────
# Check GitHub Releases: nightly = prerelease with -nightly tag in last 24h,
# weekly/stable = non-prerelease release in last 7 days.
ONE_DAY_AGO=$(date -u -v-1d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '1 day ago' '+%Y-%m-%dT%H:%M:%SZ')
SEVEN_DAYS_AGO=$(date -u -v-7d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '7 days ago' '+%Y-%m-%dT%H:%M:%SZ')
releases=$(gh api "repos/${REPO}/releases?per_page=20" --jq '[.[] | {tag_name, prerelease, draft, created_at}]' 2>/dev/null || echo "[]")
nightly_rel_ok=$(echo "$releases" | jq -r "[.[] | select(.prerelease == true and .draft == false and (.created_at > \"${ONE_DAY_AGO}\") and (.tag_name | test(\"nightly\")))] | if length > 0 then 1 else 0 end")
weekly_rel_ok=$(echo "$releases" | jq -r "[.[] | select(.prerelease == false and .draft == false and (.created_at > \"${SEVEN_DAYS_AGO}\") and (.tag_name | test(\"nightly\") | not))] | if length > 0 then 1 else 0 end")

# ── Weekly workflow ──────────────────────────────────────────────────────────
# Check for a "Weekly" type workflow from config, or fall back to hardcoded
weekly_ok=-1
if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
  weekly_wf=$(echo "${HEALTH_CHECK_WORKFLOWS}" | python3 -c "
import sys, json
wfs = json.loads(sys.stdin.read())
weekly = [w for w in wfs if w.get('type') == 'weekly']
print(weekly[0]['name'] if weekly else '')
" 2>/dev/null)
  if [ -n "$weekly_wf" ]; then
    weekly=$(gh run list --repo "$REPO" --workflow "$weekly_wf" --status completed --limit 1 \
      --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
    weekly_ok=$( [ "$weekly" = "success" ] && echo 1 || echo 0 )
  fi
else
  weekly=$(gh run list --repo "$REPO" --workflow "Weekly Coverage Review" --status completed --limit 1 \
    --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
  weekly_ok=$( [ "$weekly" = "success" ] && echo 1 || echo 0 )
fi

# ── Perf workflows — worst of all ───────────────────────────────────────────
hourly_worst=1
if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
  perf_wfs=$(echo "${HEALTH_PERF_WORKFLOWS}" | python3 -c "import sys,json; [print(w) for w in json.loads(sys.stdin.read())]" 2>/dev/null)
  if [ -n "$perf_wfs" ]; then
    while IFS= read -r wf; do
      result=$(gh run list --repo "$REPO" --workflow "$wf" --status completed --limit 1 \
        --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
      if [ "$result" = "failure" ]; then hourly_worst=0; fi
    done <<< "$perf_wfs"
  fi
else
  for wf in "Perf — React commits per navigation" "Performance TTFI Gate"; do
    result=$(gh run list --repo "$REPO" --workflow "$wf" --status completed --limit 1 \
      --json conclusion --jq '.[0].conclusion // "none"' 2>/dev/null || echo "none")
    if [ "$result" = "failure" ]; then hourly_worst=0; fi
  done
fi
hourly_ok=$hourly_worst

# ── Deploy checks ────────────────────────────────────────────────────────────
deploy_json=""
if [ -n "$CI_WF" ]; then
  deploy_run_id=$(gh run list --repo "$REPO" --workflow "$CI_WF" \
    --event push --branch main --status completed --limit 1 \
    --json databaseId --jq '.[0].databaseId' 2>/dev/null || echo "")

  if [ -n "$deploy_run_id" ]; then
    if [ "$HIVE_CONFIG_LOADED" = "true" ] && command -v python3 &>/dev/null; then
      job_count=$(echo "${HEALTH_DEPLOY_JOBS}" | python3 -c "import sys,json; print(len(json.loads(sys.stdin.read())))" 2>/dev/null || echo 0)
      if [ "$job_count" -gt 0 ]; then
        for i in $(seq 0 $((job_count - 1))); do
          job_name=$(echo "${HEALTH_DEPLOY_JOBS}" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())[$i]['name'])" 2>/dev/null)
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
