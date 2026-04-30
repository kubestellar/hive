## KubeStellar Comprehensive Hygiene

Run a full operational sweep across all project repos defined in `$HIVE_REPOS` (read from hive-project.yaml at kick time).

Execute ALL sections below in order. Use sub-agents (Agent tool) to parallelize independent checks. Use `unset GITHUB_TOKEN &&` before all `gh` commands.

**Setup:** All `gh` commands below iterate over `$HIVE_REPOS` (space-separated list of `org/repo` entries). The kick script sets this from `hive-project.yaml`.

---

### 1. Nightly & Weekly Builds

Check the latest workflow runs for each of these. Report status (pass/fail/in-progress), duration, and link.

```bash
# Check all repos for workflow runs
for repo in $HIVE_REPOS; do
  echo "=== $repo ==="
  unset GITHUB_TOKEN && gh run list --repo "$repo" --limit 10 --json name,status,conclusion,createdAt,url

  # Check for build-specific workflows (Docker, Release, Helm, Brew)
  for wf in "Docker Build" "Release"; do
    unset GITHUB_TOKEN && gh run list --repo "$repo" --workflow "$wf" --limit 3 --json name,status,conclusion,createdAt,url 2>/dev/null
  done

  # Helm chart builds
  unset GITHUB_TOKEN && gh run list --repo "$repo" --limit 20 --json name,status,conclusion,createdAt | jq '[.[] | select(.name | test("helm|chart"; "i"))]'

  # Brew builds
  unset GITHUB_TOKEN && gh run list --repo "$repo" --limit 20 --json name,status,conclusion,createdAt | jq '[.[] | select(.name | test("brew|formula"; "i"))]'
done
```

Report a table of all workflow runs with any failures highlighted.

---

### 2. CI Status

For each repo, check if CI is green on the main/default branch:

```bash
for repo in $HIVE_REPOS; do
  echo "=== $repo ==="
  unset GITHUB_TOKEN && gh run list --repo "$repo" --branch main --limit 5 --json name,status,conclusion,createdAt
done
```

Flag any failing CI on main — these are critical.

---

### 3. PR Review

For each repo, list all open PRs with their status, CI checks, review state, and age:

```bash
for repo in $HIVE_REPOS; do
  echo "=== $repo ==="
  unset GITHUB_TOKEN && gh pr list --repo "$repo" --state open --json number,title,author,createdAt,reviewDecision,statusCheckRollup,labels,url
done
```

For each open PR:
- Note CI status (passing/failing/pending)
- Note review status (approved/changes requested/pending)
- Note age (flag if >7 days old)
- Note if it has Copilot review comments that need addressing
- Recommend action: merge, needs review, needs fixes, or stale

---

### 4. Issue Review

For each repo, list all open issues:

```bash
for repo in $HIVE_REPOS; do
  echo "=== $repo ==="
  unset GITHUB_TOKEN && gh issue list --repo "$repo" --state open --json number,title,labels,createdAt,author,url
done
```

For each issue:
- Assess if it's legitimate, noise, or already fixed
- Check if there's already a PR addressing it
- Flag duplicates
- Recommend: fix, close as noise, close as duplicate, or needs triage

---

### 5. Nightly Issue Findings

Check for issues created by nightly workflows (typically labeled `nightly`, `automated`, `triage-needed`, or created by `github-actions`):

```bash
# Check primary repo for nightly findings (adapt repo if nightlies run elsewhere)
PRIMARY_REPO="${HIVE_REPOS%% *}"  # first repo in list
unset GITHUB_TOKEN && gh issue list --repo "$PRIMARY_REPO" --state open --label "triage-needed" --json number,title,createdAt,labels,url
unset GITHUB_TOKEN && gh issue list --repo "$PRIMARY_REPO" --state open --label "nightly" --json number,title,createdAt,labels,url 2>/dev/null
unset GITHUB_TOKEN && gh issue list --repo "$PRIMARY_REPO" --state open --search "author:github-actions" --json number,title,createdAt,labels,url
```

For each nightly finding:
- Categorize severity: critical (broken functionality), medium (degraded), low (cosmetic/noise)
- For critical items: investigate and fix immediately (create worktree, fix, PR)
- For noise: close with explanation
- For medium: note for follow-up

---

### 6. Branch & Ref Cleanup

For each repo found in `/tmp/` matching project repo names (main repos only, not worktrees):

1. **Pull main** if on main branch
2. **Prune** stale remote tracking refs: `git remote prune origin`
3. **Delete `[gone]` branches** that do NOT have associated worktrees

Write a discovery script to `/tmp/ks-hygiene-discover.sh` and run with `/opt/homebrew/bin/bash`:

```bash
#!/usr/bin/env bash
declare -A seen
for d in /tmp/*; do
  [ -d "$d/.git" ] || continue
  remote=$(cd "$d" && git remote get-url origin 2>/dev/null)
  # Match any repo in $HIVE_REPOS
  matched=false
  for repo in $HIVE_REPOS; do
    [[ "$remote" == *"github.com/$repo"* ]] && matched=true && break
  done
  $matched || continue
  if [ -z "${seen[$remote]}" ] || [ ${#d} -lt ${#seen[$remote]} ]; then
    seen[$remote]="$d"
  fi
done
for remote in "${!seen[@]}"; do
  echo "${seen[$remote]}|$remote"
done
```

Then for each discovered repo:
```bash
cd <repo_path>
# Pull main
current=$(git branch --show-current)
[ "$current" = "main" ] && git pull --rebase origin main

# Prune
git remote prune origin

# Delete gone branches (skip those with worktrees)
git branch -v | grep '\[gone\]' | sed 's/^[+* ]*//' | awk '{print $1}' | while read branch; do
  wt=$(git worktree list | grep "\[$branch\]" | awk '{print $1}')
  if [ -z "$wt" ]; then
    git branch -D "$branch"
  fi
done
```

---

### 7. Deployment Health (vllm-d & pok-prod)

Check the latest "Build and Deploy KC" workflow for deployment status to both clusters:

```bash
# Latest deploy workflow run
unset GITHUB_TOKEN && gh run list --repo ${PROJECT_PRIMARY_REPO} --workflow "Build and Deploy KC" --limit 5 --json name,status,conclusion,createdAt,url

# Job-level detail for latest run
LATEST_RUN=$(unset GITHUB_TOKEN && gh run list --repo ${PROJECT_PRIMARY_REPO} --workflow "Build and Deploy KC" --branch main --limit 1 --json databaseId --jq '.[0].databaseId')
unset GITHUB_TOKEN && gh run view "$LATEST_RUN" --repo ${PROJECT_PRIMARY_REPO} --json jobs --jq '.jobs[] | "\(.name) | \(.status) | \(.conclusion)"'
```

For each deployment target:
- **deploy-pok-prod**: Should be PASS. If failing, check logs.
- **deploy-vllm-d**: Known blocker — GitHub PAT in the deploy secret expires periodically. If failing with RBAC error, flag as "needs VPN + PAT renewal".

Also check if clusters are reachable (best-effort, depends on VPN):
```bash
kubectl --context vllm-d cluster-info 2>&1 | head -3
kubectl --context pok-prod cluster-info 2>&1 | head -3
```

---

### 8. Helm Chart & Brew Formula Currency

Verify the Helm chart and Homebrew formulas are up-to-date with the latest releases.

```bash
# Latest weekly release
unset GITHUB_TOKEN && gh release list --repo ${PROJECT_PRIMARY_REPO} --limit 5 --json tagName,createdAt,isPrerelease --jq '[.[] | select(.isPrerelease == false)] | .[0]'

# Latest nightly release
unset GITHUB_TOKEN && gh release list --repo ${PROJECT_PRIMARY_REPO} --limit 5 --json tagName,createdAt,isPrerelease --jq '[.[] | select(.isPrerelease == true)] | .[0]'

# Brew formula versions
# Brew formula versions (adapt paths for your project's homebrew tap)
# unset GITHUB_TOKEN && gh api repos/${PROJECT_ORG}/homebrew-tap/contents/Formula/<formula>.rb --jq '.content' | tr -d '\n' | base64 -d | grep 'version "'

# Helm Chart Release workflow
unset GITHUB_TOKEN && gh run list --repo ${PROJECT_PRIMARY_REPO} --workflow "Helm Chart Release" --limit 3 --json status,conclusion,createdAt,url

# Release workflow (creates nightly + weekly releases + updates brew)
unset GITHUB_TOKEN && gh run list --repo ${PROJECT_PRIMARY_REPO} --workflow "Release" --limit 3 --json status,conclusion,createdAt,url
```

Flag:
- **Brew formula stale**: if formula version < latest weekly release version
- **kc-agent formula stale**: if version < latest nightly release version
- **Nightly missing**: if today's date has no nightly release AND the Release workflow failed
- **Helm chart release failures**: any recent Helm Chart Release workflow failures

---

### 9. Summary Report

Output a comprehensive summary with:

1. **Build Health** table — all workflow runs with status
2. **CI Status** table — main branch CI per repo
3. **Open PRs** table — with recommended actions
4. **Open Issues** table — with triage recommendations
5. **Nightly Findings** — critical/medium/low with actions taken or recommended
6. **Branch Cleanup** table — pruned refs, deleted branches, skipped branches per repo
7. **Deployment Health** table — vllm-d and pok-prod deploy status, any blockers
8. **Distribution Currency** table — brew formula versions vs latest releases, nightly release status, helm chart release status

---

### Rules

- **NEVER delete worktrees** — user rule, no exceptions
- **NEVER push to any remote** — unless fixing a critical nightly finding (then use worktree + PR workflow)
- **NEVER merge PRs on project repos without user approval** — present recommendations, don't auto-merge
- **NEVER commit directly to main** — always use feature branches
- **Ignore Playwright failures** — not blocking for PRs
- **Ignore `llm-d-deployer`** — archived/read-only
- **Use `unset GITHUB_TOKEN &&` before all `gh` commands**
- **Use `/opt/homebrew/bin/bash` for scripts with associative arrays** (zsh compat)
- **Use sub-agents to parallelize** sections 1-5 where possible
- Report any errors encountered but continue processing
- For fixes: use git worktree workflow, DCO-signed commits, emoji PR titles
