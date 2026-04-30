# Architect Skill: Live Status via Beads

Load this when maintaining the live status bead during a pass, or when the dashboard is showing stale architect status.

## Live Status via Beads — MANDATORY

The dashboard shows your current work to the operator. It reads your in-progress bead title as your live status. **You MUST maintain an in-progress bead at all times during a pass.**

```bash
# At pass start
cd /home/dev/architect-beads && bd create --title "Architect: implementing container-query rollout for #8695" --type task --status in_progress

# As work progresses — update title to reflect current action
cd /home/dev/architect-beads && bd update <bead_id> --title "Architect: PR #10051 opened, waiting for CI"

# At pass end
cd /home/dev/architect-beads && bd update <bead_id> --status done --notes "Pass complete: PR #10051 merged"
```

Without this, the dashboard shows stale status from hours ago. The operator cannot see what you are doing.

## Status Reporting — MANDATORY

Write `~/.hive/architect_status.txt` at the **start of every sub-action** so the dashboard shows what you are doing right now. Update before every `gh`, `git`, `curl`, or file-read operation that might take more than a few seconds. The dashboard polls every 30 seconds.

**STATUS field must be one of these values:**
- `DONE` — task/pass complete, evidence attached
- `DONE_WITH_CONCERNS` — task complete but flagging a risk (explain in EVIDENCE)
- `NEEDS_CONTEXT` — blocked on missing information (specify what in EVIDENCE)
- `BLOCKED` — hard blocker (specify what and who can unblock in EVIDENCE)
- `WORKING` — actively executing (default during a pass)

```bash
cat > ~/.hive/architect_status.txt <<EOF
AGENT=architect
STATUS=WORKING
TASK=<one-line description of current work>
PROGRESS=Step N/M: <what you are doing now>
RESULTS=<comma-separated findings so far — use ✓ for complete, ✗ for blocked>
EVIDENCE=<verification output or blocker details>
UPDATED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
```

**Required write points (write at the START of each, not after):**

| When | TASK | PROGRESS example |
|------|------|-----------------|
| Pass start | Starting architect pass | scanning issues and PRs |
| Before each `gh issue list` / `gh pr list` | Fetching issues/PRs | fetching open issues from ${PROJECT_PRIMARY_REPO} |
| Before reading each source file | Reading source | reading pkg/api/handler.go |
| Before opening issue | Opening tracking issue | opening issue: <slug> |
| After issue opened | Building fix | opened #N — implementing fix |
| Before opening PR | Opening PR | opening PR for issue #N |
| After PR opened | Monitoring CI | PR #N awaiting CI (build, lint) |
| Before merging | Merging PR | merging PR #N (CI passed) |
| Pass complete | Pass complete | done — merged #N, #N |
