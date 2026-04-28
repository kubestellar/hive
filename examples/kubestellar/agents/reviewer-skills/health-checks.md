# Reviewer Skill: Health Check Monitoring

Load this when checking CI health, fixing failing workflows, or monitoring deployment health.

## Health Check Monitoring — every pass — FIX MANDATORY

You own the health panel on the hive dashboard. Every pass, check these and **fix them via PRs**. Do NOT just report red checks — your job is to make them green:

```bash
# Run the health check script
/tmp/hive/dashboard/health-check.sh
```

This returns JSON with: `ci`, `brew`, `helm`, `nightly`, `nightlyRel`, `weekly`, `weeklyRel`, `hourly`, `vllm`, `pokprod` (1=ok, 0=fail, -1=unknown).

**When a check is red (0) — you MUST fix it via PR, not just report it:**

1. Pull the failed workflow's logs:
   ```bash
   unset GITHUB_TOKEN && gh run list --repo kubestellar/console --workflow "<workflow name>" --limit 1 --json databaseId,conclusion --jq '.[0]'
   unset GITHUB_TOKEN && gh run view <run_id> --repo kubestellar/console --log-failed 2>&1 | tail -80
   ```
2. Diagnose the root cause from the logs
3. **Fix it yourself** — create a branch, fix the workflow or test code, open a PR:
   ```bash
   git checkout -b fix/nightly-<description>
   # Make the fix
   git add -A && git commit -s -m "🐛 Fix <workflow name>: <root cause>"
   unset GITHUB_TOKEN && gh pr create --title "🐛 Fix <workflow name>: <root cause>" \
     --body "The <workflow name> workflow has been failing since <date>.\n\nRoot cause: <explanation>\nFix: <what you changed>"
   ```
4. Send ntfy: `"Fixed <workflow>: <root cause>. PR #<N>"`

**Do NOT just file issues for red checks.** Your job is to fix them. File an issue only if the fix requires infrastructure changes you cannot make (e.g., secrets, runner config).

### Workflows you own (FIX when red)

| Category | Workflows | Dashboard indicator |
|----------|-----------|-------------------|
| **Nightly** | Nightly Test Suite, Nightly Compliance & Perf, Nightly UX Journeys, Nightly Dashboard Health, Nightly DAST, Card Standard Nightly, Playwright Nightly | `nightly` |
| **Hourly/Perf** | Perf — React commits per navigation, Perf TTFI Gate, Perf bundle size, Perf React commits idle | `hourly` |
| **CI** | All PR check workflows (build, lint, test, ui-ux-standard, nil-safety) | `ci` |
| **Weekly** | Weekly Coverage Review | `weekly` |
| **Deploys** | Build and Deploy KC (vLLM-d job, PokProd job) | `vllm`, `pokprod` |

## Brew Formula Check — every pass

Check `kubestellar/homebrew-tap` for staleness every pass:

```bash
# Console formula version
unset GITHUB_TOKEN && gh api repos/kubestellar/homebrew-tap/contents/Formula/kubestellar-console.rb \
  --jq '.content' | base64 -d | grep '^\s*version'

# Latest kubestellar/console release (non-draft)
unset GITHUB_TOKEN && gh release list --repo kubestellar/console --limit 5 \
  --json tagName,publishedAt,isDraft --jq '[.[] | select(.isDraft==false)] | .[0]'
```

If formula version ≠ latest release tag → file a P2 bead + ntfy (topic: `$NTFY_SERVER/$NTFY_TOPIC`, priority: default).

## vllm-d and pok-prod01 deployment health

Check the last 5 runs of the `Build and Deploy KC` workflow:

```bash
unset GITHUB_TOKEN && gh run list --repo kubestellar/console --workflow "Build and Deploy KC" --limit 5 --json databaseId,conclusion,status,createdAt
# Then: gh run view <id> --repo kubestellar/console --json jobs --jq '.jobs[] | select(.name | test("vllm|pok"; "i")) | {name, conclusion, status}'
```

Any failure → high ntfy + regression issue + bead P1.

Also verify the deployed version matches the latest stable release tag for pok-prod01.

## Helm chart freshness

`deploy/helm/Chart.yaml` `appVersion` must match latest stable console release:

```bash
unset GITHUB_TOKEN && gh api /repos/kubestellar/console/contents/deploy/helm/Chart.yaml --jq '.content' | base64 -d | grep 'appVersion\|version'
```

Mismatch → high ntfy + file issue on `kubestellar/console` + dispatch fix agent to bump Chart.yaml.
