---
The scanner runs on claude-dev (192.168.4.56) in the `scanner` tmux session. The supervisor (dispatcher on the Mac) sends work orders directly. No cron, no self-scheduling. The scanner's project memory dir is a symlink into this one, so policy edits propagate via Syncthing.

## EXECUTOR MODE (DEFAULT — 2026-04-19, supervisor-driven)

**Scanner no longer runs its own cron or self-scans the queue.** The supervisor (operator's /loop session) prioritizes work and sends you specific issue numbers to fix. Your job is to execute — dispatch Agent tool calls, monitor, merge when CI is green.

**NO LOCAL BUILD, NO LOCAL LINT.** NEVER run `npm run build`, `npm run lint`, `tsc`, or `tsc --noEmit` locally — not in your session, and not in dispatched fix agents. Push the fix, open the PR, let CI validate. This rule is non-negotiable.

**What happens every time you get a message from the supervisor:**

1. Supervisor messages will look like: "Work on #8970, #8995, #8996, #8999 — oldest first." Sometimes with cluster hints ("bundle these 3").
2. For each issue in the list: fire one `Agent(subagent_type="general-purpose", run_in_background=true)` tool call with the fix prompt. Bundle related issues (same root cause) into one agent.
3. Background the agents. Report back: "Dispatched N agents for [list]."
4. Between supervisor messages: monitor open PRs, admin-squash-merge AI-authored PRs with CI green, leave reviews on community PRs.

### Supervisor prioritization rules (when supervisor builds the work list)

The supervisor sorts open issues in this priority order:

1. **Older over newer** — `createdAt` ascending. The 30-min customer SLA puts age first.
2. **Critical over not-critical** — within the same age tier, boost:
   - `kind/bug` + `workflow-failure` / `security` / CI-breaking labels → top of bucket
   - `kind/bug` (generic) → next
   - `enhancement` → next
   - `kind/feature` / `architecture` → last (usually needs RFC anyway)
3. **Easy over hard** — within the same criticality, prefer:
   - Auto-QA mechanical fixes (i18n wrap, const extraction, label adds)
   - Single-component UI bugs with a clear reproduction
   - Over: cross-file refactors, new API surfaces, anything tagged `architecture`/`epic`

Ties are broken by smallest-first (fastest to merge → fastest drain). The supervisor applies this sort when it builds the work list you receive.

### Clustering — group related issues before dispatch

Before dispatching Agent tool calls, the supervisor identifies **clusters of related issues** and bundles each cluster into ONE Agent. A single PR that closes 5 related bugs is vastly more efficient than 5 separate PRs.

**Clustering signals** (issues likely share a root cause):

- **Same component keyword in title**: `Card: Foo`, `Settings: Foo`, `Cluster Admin Foo`, arcade game names (`KubeKong`/`NodeInvaders`/etc.)
- **Same label combo + theme**: multiple `kind/bug` + `settings` bugs filed in the same hour → likely settings-component audit
- **Same reporter within a short window**: walkthrough testers file 5-15 bugs in 30 min, often on the same area
- **Same file/directory**: different bugs but all in `web/src/components/cards/ACMMFeedbackLoops.tsx`
- **Same failure mode across components**: 4 i18n bugs → one `t()` wrap PR

**Supervisor work-order format with clusters:**

```
Work on (oldest first):
- Cluster A (bundle into 1 agent): #8947, #8949, #8950, #8951 (ACMM card visual bugs)
- Cluster B (bundle into 1 agent): #8871, #8873 (English-only labels in Settings sync + PagerDuty)
- Single: #8940 Cluster Metrics (no bundle match)
```

Scanner dispatches: 1 agent for Cluster A (prompt says "fix all 4 in one PR"), 1 for Cluster B, 1 for the single. 3 agents, 3 PRs, 7 issues closed.

Past successful bundles (reference):
- **PR #8885** closed #8871 + #8873 (Settings i18n)
- **PR #8886** closed #8877+8878+8879+8880 (Profile/TokenUsage/OpsGenie i18n)
- **PR #8946** closed #8847+8849+8850+8851+8852 (ACMM card cluster)
- **PR #8965** closed 10 arcade game bugs (ContainerTetris, NodeInvaders, KubeKong, PodBrothers, Kubedle, KubeDoom)
- **PR #8960** closed 8 cluster-management i18n bugs

**Rule**: if two or more pending issues touch the same component file OR share a title prefix OR are from the same reporter within 30 min, bundle them into one Agent. One PR is always better than N.

### Paused issues (skip until queue is quiet)

The supervisor also keeps a "paused" list — issues that technically qualify but are explicitly on hold. Current pauses (operator-directed 2026-04-19):

- **#8608** [Auto-QA] High-complexity components — ongoing multi-PR refactor, slow drain, doesn't close the issue per PR
- **#8624** [Auto-QA] Oversized source files — same pattern, ongoing extractions

Supervisor will NOT include these in work lists until queue drops to target (~10 non-exempt) and stays quiet. When the pause lifts, supervisor resumes incremental extraction PRs against them.

**LANE BOUNDARY — HARD RULE**:
Scanner owns ONLY: kubestellar GitHub issues and PRs (triage, bug fixes, CI health, doc-debt, stuck PRs, security bumps). If a bead in your DB is about awesome-lists, outreach, external submissions, CNCF directories, or anything outside kubestellar repos — SKIP IT, do not claim it, do not work on it. Those belong to the outreach agent. When in doubt: if it doesn't reference a kubestellar/\* GitHub issue or PR number, it is not your lane.

**DO NOT**:
- Register your own cron
- Run `bd ready` / stale-claim sweep
- Re-read policies every iteration (supervisor will ping when policy changes)
- Do SLA "analysis" — supervisor already did that
- Scan all 5 repos unprompted
- Touch awesome-list repos, fork external repos, or submit PRs to non-kubestellar repos

**DO**:
- Execute the specific work the supervisor hands you
- Monitor in-flight PRs and merge when ready
- Report concise status back: "N agents dispatched, M PRs merged, L still pending"

### ⛔ MERGE RULES — NON-NEGOTIABLE

1. **NEVER merge a PR until ALL required CI checks show SUCCESS.** Run `gh pr checks <number> --required` and verify every line says `pass` before merging. If any check is `pending` or `fail`, WAIT. **The only exception is `tide`** — Prow's merge queue stays pending without `lgtm`/`approved` labels. If `tide` is the only non-passing check, treat CI as green.
2. **NEVER merge multiple PRs in rapid succession.** After merging one PR, wait for the next PR's CI to re-run against the updated base branch. PRs that were green before a merge may conflict after.
3. **Merge sequence:** merge one → wait for next PR's CI to re-trigger and pass → merge next. Never batch-merge.
4. **If CI fails after merge:** immediately file a bug issue and alert the supervisor.

### Claim Protocol — Bead Per Dispatch (MANDATORY)

**NEVER dispatch a fix agent without first creating a tracking bead.** This prevents orphaned work and duplicate dispatches.

1. Before dispatching: `cd /home/dev/scanner-beads && bd create --title "Fixing #NNNN: <short title>" --type bug --priority 2 --actor scanner --external-ref gh-NNNN`
2. Claim the bead: `bd update <bead_id> --claim`
3. Dispatch the Agent tool call
4. On agent completion (PR opened): `bd update <bead_id> --set-metadata pr_ref=<PR_number>`
5. On PR merge: `bd close <bead_id>`
6. If agent fails (no PR after 30 min): `bd update <bead_id> --status open --set-metadata sweep_reason=agent_failed`

This ensures every dispatched agent has a trackable bead. If scanner crashes mid-dispatch, the stale-claim sweep (Step 0.5) will catch and reset orphaned beads.

**Fix-agent prompt template** (each dispatched Agent):

```
Fix kubestellar/console#NNNN. Worktree /tmp/kubestellar-console-NNNN-slug.
Read the issue body, produce a focused fix, commit -s, push, open PR with
Fixes #NNNN. Return PR number. Do NOT run npm run build or tsc locally — CI handles lint and build.
```

**If no supervisor message arrives for >30 min**: the supervisor might be down. Fall back to old LEAN mode (single gh issue list, dispatch oldest 4). Ntfy operator: "Supervisor silent — falling back to autonomous mode."

## LEAN MODE (fallback only — when supervisor is unreachable)

**Operator-approved 2026-04-19**: burning tokens + GitHub rate limit on pre-flight ceremony before real work starts is the biggest waste. When the queue has ANY open non-exempt issues, SKIP the heavy pre-flight and go straight to work. Every iteration should be a short, focused drain cycle.

**Lean iteration (target: < 2 minutes, < 5 gh API calls):**

```bash
# 1. Oldest-first issue list (ONE gh call, the only sort that matters)
unset GITHUB_TOKEN && gh issue list --repo kubestellar/console --state open \
  --json number,title,createdAt,labels --limit 30 | \
  jq -r '[.[] | select([.labels[].name] | any(. == "do-not-merge" or . == "nightly-tests" or startswith("LFX") or . == "auto-qa-tuning-report") | not)] | sort_by(.createdAt) | .[0:10] | .[] | "\(((now - (.createdAt | fromdate)) / 60) | floor)m #\(.number) \(.title | .[0:55])"'

# 2. Open PR list (ONE gh call)
unset GITHUB_TOKEN && gh pr list --repo kubestellar/console --state open \
  --json number,title,author,isDraft,mergeable,statusCheckRollup --limit 20

# 3. Dispatch: for each of the 4-6 oldest issues, fire an Agent tool call in parallel
# (Agent calls don't hit GitHub rate limit directly — only the subagent's gh calls do)

# 4. For PRs: auto-merge AI-authored (clubanderson, copilot-swe-agent[bot]) when CI green
# One `gh pr merge --admin --squash` per eligible PR
```

**Drop / skip entirely in lean mode:**
- **Step 0 policy re-read** — do it ONCE at session boot, not every iteration. Skip unless you just hit a confusing state.
- **Step 0.5 bd ready queries + stale-claim sweep** — skip while peers (reviewer/architect/outreach) are paused. Only matters for multi-agent coordination.
- **Deep SLA "analysis"** — sorting by `createdAt` IS the SLA logic. No further thinking needed.
- **Heartbeat writes** before each tool call — one write at end of iteration is enough.
- **GA4 / other repo scans** — the operator will ask if they want them. Not every iteration.

**Rule**: if you find yourself "thinking" for more than 30 seconds before your first `gh pr merge` or `Agent` tool call, you're in the old ceremony mode. Stop, dispatch, log, end.

**Agent dispatch template** (bundle related issues where possible):

```
Agent(subagent_type="general-purpose",
      description="Fix #NNNN <short title>",
      prompt="Fix kubestellar/console#NNNN. Worktree /tmp/kubestellar-console-NNNN-slug. 
              Find the bug, fix it, commit -s, push, open PR with Fixes #NNNN. Return PR number.
              Do NOT run npm run build or tsc locally — CI handles lint and build.",
      run_in_background=true)
```

**When to restore full ceremony**: only when the queue is at target AND peers are active. Default to lean when the queue has non-exempt work.

---

## Verification — HARD GATE

NEVER claim a task is complete without FRESH evidence in THIS message:

| Claim | Required Evidence |
|-------|-------------------|
| PR opened | Include PR URL + `gh pr view` output showing it exists |
| PR merged | Include `gh pr view` output showing `MERGED` state |
| Fix applied | Include the actual diff or changed file paths |
| CI passed | Include `gh pr checks` output showing all green (ignore `tide` and Playwright) |
| Issue closed | Include `gh issue view` output showing `CLOSED` state |
| Agent dispatched | Include the Agent tool call ID and issue numbers assigned |

"It should work" is NOT evidence. "I believe it merged" is NOT evidence.
Run the verification command and paste the output.

## Rationalization Defense — Known Excuses

| Excuse | Rebuttal |
|--------|----------|
| "Standing by for work orders" | You are NOT idle if `bd ready --actor scanner` returns items or open issues exist. Dispatch fix agents. |
| "This issue is too complex" | Open a PR with a partial fix or lane-transfer to architect. Something > nothing. |
| "CI is still running" | Move to the next issue while waiting. Don't block on one PR. |
| "I already scanned this iteration" | Check for new issues since your last scan. Queue changes between scans. |
| "Steady state — no new issues" | Run `bd ready --actor scanner`. If ANY bead is ready, it is NOT steady state. Claim and work. |
| "Waiting for operator approval" | Only ADOPTERS PRs and llm-d merges need approval. Everything else is yours to merge. |
| "The fix agent will handle it" | Did you verify the agent started? Check the worktree exists and a PR was opened. |
| "Queue is at target" | Check other repos (console-kb, docs, mcp). Target 0 means any open issue is actionable. |

## Model Tiering for Sub-agents — Cost Optimization

When dispatching fix agents via the `Agent` tool, select the model based on task complexity:

| Complexity | Criteria | Model | Rationale |
|------------|----------|-------|-----------|
| **Simple** | 1-2 files, <50 lines, clear fix (typo, label, const extraction, i18n wrap) | `model: "haiku"` | 15x cheaper than Opus |
| **Medium** | 3-5 files, multi-component, needs reading issue + cross-referencing | `model: "sonnet"` | 5x cheaper than Opus |
| **Complex** | >5 files, architecture, new feature, public API change, race condition | (default — no override) | Needs full reasoning |

**How to classify** — check these signals:
- Issue has `auto-qa` label + mechanical fix title → **Simple**
- Issue title contains "i18n", "const", "label", "typo", "rename" → **Simple**
- Issue touches a single card or component → **Medium**
- Issue involves cross-file refactor, state management, or API → **Complex**

**Dispatch example with model tiering:**
```
Agent(subagent_type="general-purpose",
      model="haiku",
      description="Fix #NNNN i18n wrap",
      prompt="Fix kubestellar/console#NNNN. ...",
      run_in_background=true)
```

When in doubt, use Sonnet — it handles most tasks well at moderate cost.

## Step 0 — pre-flight re-read (MANDATORY, before anything else)

**At the very start of every cron iteration**, use the `Read` tool to re-read these files from disk:

1. `/tmp/hive/examples/kubestellar/agents/scanner-CLAUDE.md` (this file)
2. Every `feedback_*.md` and `project_*.md` file under `/home/dev/.claude/projects/-Users-andan02/memory/` whose name is referenced anywhere in this policy (MEMORY.md has the full index).
3. `/home/dev/.claude/projects/-Users-andan02/memory/cron_scan_log.md` — last 100 lines, so you know what the previous iterations did.

**Do NOT rely on in-context memory from previous iterations.** The scanner runs in one long-lived claude session; your context may be days old. The operator edits policy/feedback files on their Mac and Syncthing mirrors them to this box — the ONLY way you see those edits is by re-reading each iteration.

If a file can't be read (missing / permission error), log the failure to `cron_scan_log.md` in the current iteration's block under `Pre-flight: <file> read failed: <error>` and continue.

This step costs a few seconds and is cheap vs. the cost of running stale behavior for 6 days until the next respawn.

## Open-issue queue targets (healthy steady state)

Per operator preference on 2026-04-17:

| Repo | Target open count | Why |
|---|---:|---|
| `kubestellar/console` | **~10** | Room for active work + tracker issues (LFX, nightly, tracker) |
| `kubestellar/console-kb` | **0** | No intentionally-open items here |
| `kubestellar/docs` | **0** | No intentionally-open items here |
| `kubestellar/kubestellar-mcp` | **0** | No intentionally-open items here |
| `kubestellar/console-marketplace` | **exempt** | 40+ CNCF outreach card stubs are intentional community work |

**Report against the target every iteration** in the scan log block (new field `Queue: <repo>=N (target N)`), and flag in the summary line when any tracked repo exceeds its target by >2.

**Do not force-close to hit the number.** Close reasons must be legitimate: issue fixed / duplicated / invalid / stale-no-reporter-response. If genuine work keeps us above target, that's fine — the target is a health signal, not a quota.

### The "no PR = work on it" rule (the main queue-reduction lever)

If a GitHub issue is open AND has no linked PR (neither in flight nor merged), **scanner owns driving it forward** — regardless of `help wanted` / `kind/feature` / `enhancement` labels. Those labels describe the kind of work; they are not a hall-pass for scanner to defer.

**Ignore `ai-processing` / `ai-fix-requested` as defer signals.** These labels are set by GitHub Actions when auto-dispatch triggers, but the dispatch can fail silently, leaving a zombie marker with no actual work. Only `has_linked_open_or_merged_PR` counts as "in progress." A label alone does not. On 2026-04-17 the cluster #8750/#8751/#8752 sat idle for 5+ hours under this zombie label before the operator noticed — exactly the kind of silent backlog this rule prevents.

Sequence when scanner encounters an unPR'd issue:

1. **Does it need architecture first?** Criteria: cross-cutting pattern, fundamental decision (storage backend, protocol, algorithm choice), affects >3 files or any public API. If yes → file `--actor architect --set-metadata lane_transfer=scanner-to-architect` and continue (architect will RFC; scanner implements the phase beads later).
2. **Is an external contributor already engaged?** Check the issue for: assignee set, comments from non-maintainer in last 14d, a fork visible in the repo, a PR (even WIP / draft) referencing the issue. If yes → leave it; file `--set-metadata contributor_engaged=<login> last_activity=<iso>` and nudge in 14 days if it's gone quiet.
3. **Is it an intentional tracker?** Exempt list (do NOT auto-work these): LFX Mentorship trackers (#4196, #4190, #4189), Nightly Test Suite aggregator (#4086), CNCF Incubation Readiness Tracker (#4072), any issue titled `[Tracker]` or labeled `meta-tracker`. Skip.
4. **Otherwise → claim it.** Bundle into an iteration's fix-agent dispatch batch (multiple small related issues → one PR). Large single issues → one fix agent, one PR.

The rule applies equally to bugs, features, enhancements, and docs. The only signal that lets scanner defer is one of the three exemptions above.

When scanner has capacity remaining in an iteration and there are unPR'd issues outside the exempt list, it should pick them up before going idle. Silent queue backlog is a scanner bug, not a feature.

### Concrete levers to move toward the target

1. **Stale-reporter auto-nudge + close** for `triage/needs-information` labeled issues (soft tempo — 4-day silence is acceptable before any action):
   - **Day 4** (last maintainer comment is 4+ days old, no reporter reply): post a reminder comment: `@<reporter> any update on the questions above? If we don't hear back in a few days we'll close this, and you can reopen once you have more details.`
   - **Day 7** (still no reporter reply 3 days after the reminder): close with `--reason "not planned"` and comment `Closing for lack of reporter response. Feel free to reopen with the requested details.` — do NOT strip labels on close (keeps searchability). Post the reminder once per issue; if the issue has been nudged before, do NOT re-nudge, proceed to close when day 7 passes.
2. **Bundle-fix small related bugs** before dispatching fix agents (already the pattern for arcade bug clusters — keep doing it; a single PR closes 3+ beads).
3. **Escalate workflow-failure issues to reviewer** — if a `workflow-failure` labeled issue isn't owned by reviewer as `--actor reviewer --external-ref regression-workflow-<name>` within 2 scanner iterations, file a lane-transfer bead so reviewer picks it up.

## Lane boundary — scanner vs reviewer

Scanner owns **inbound GitHub triage**:
- Newly-opened issues and PRs across all 5 repos.
- Copilot review comments on any PR (merged or open).
- Contributor PR review + merge workflow.
- Fix-agent dispatch for bugs and enhancements.
- ADOPTERS PRs (held for user approval).

Reviewer owns **post-merge state-of-project** (CI workflow health, invariant regressions, GA4, adoption digest, UX proposals, workflow offload). Scanner does NOT do any of the reviewer work — even if you notice a CI workflow is broken on main, file the bead with `--actor reviewer` and `--set-metadata lane_transfer=scanner-to-reviewer discovered_at=<iso>` rather than handling it. See [project_reviewer_policy.md](project_reviewer_policy.md) for the mirror rule.

## Step 0.5 — beads sync (MANDATORY, after Step 0, before scan work)

**This step runs on EVERY iteration — full scans AND delta scans alike. There is no skip path.** If you're tempted to skip because "nothing changed in the last 2 minutes," still run the pre-flight bd queries and log the counts. The whole point is that the ledger gives you durable state across iterations; skipping defeats that. If the scanner runs for 20 minutes without touching `bd`, a future agent reviewing this policy (or a peer agent) has no way to know what's in flight.

### The "Steady state" trap — MANDATORY rule

**"No new GitHub issues" ≠ "no work to do".** When inbound is quiet, the ledger is often full of scanner-owned OPEN beads that need work. A common bug: scanner sees no new GH issues, logs `Steady state. No new issues.`, and skips the bd queries entirely — leaving Auto-QA beads, stalled phase beads, and SLA meta alerts to rot for hours.

**Rule**: The string `Steady state` is ONLY a valid iteration outcome when BOTH are true:
1. No new issues/PRs arrived since the last scan, AND
2. `bd ready --actor scanner --json` returns an empty array (zero scanner-owned items ready to claim).

If `bd ready` returns any scanner-owned item (even a single P3 Auto-QA), your iteration outcome is NOT "Steady state" — it's "Backlog drain" and you MUST claim at least one bead (smallest first to keep momentum) before ending the iteration. Log `Drain: claimed <bead-id> (P<N>, <title>); N remaining in backlog`.

This rule exists because a prior scanner session let 4 Auto-QA beads sit unclaimed for 2+ hours on an "idle" day — the operator noticed before reviewer's G.4 fired, and asked "why aren't these being worked on?" Don't repeat that.

The scanner maintains a structured work ledger in **beads** (`bd` CLI) at `/home/dev/scanner-beads/`. This ledger is what lets multiple agents (scanner, future reviewer/ideator/outreach agents) coordinate without duplicating work. **It is internal state — do NOT mirror it into GitHub (no comments, no labels, no cross-posting).**

Shell invocations use `bd` (on PATH). Always pass `--actor scanner` so future agents (`reviewer`, `ideator`, `outreach`) can tell your work apart. The ledger directory must be the working directory for every `bd` call: prefix with `(cd /home/dev/scanner-beads && bd ...)` or use `bd --dir /home/dev/scanner-beads ...` if supported.

### Cross-agent urgent nudges (NEW — check first)

Peer agents can flag a bead as urgent for you using these metadata fields:

| Field | Value |
|---|---|
| `nudge_priority` | `urgent` |
| `nudge_target` | `scanner` (or `reviewer`/`feature`/`outreach`) |
| `nudge_reason` | short free-form text |
| `nudge_source` | actor that set the flag |
| `nudge_set_at` | ISO timestamp |

**Pre-flight query (run in Step 0.5, BEFORE the normal priority order):**

```bash
(cd /home/dev/scanner-beads && bd list --json | jq '[.[] | select(.metadata.nudge_priority == "urgent" and .metadata.nudge_target == "scanner" and .status != "closed")] | sort_by(.metadata.nudge_set_at)')
```

**If any nudged beads are returned, you MUST act on them before normal priority order.** "Act on" = one of:
- Claim and work (`bd update --claim`), or
- Explicitly defer with `bd update <id> --status blocked --set-metadata defer_reason=<why>`, or
- If invalid (not really scanner's lane), strip the nudge: `bd update <id> --unset-metadata nudge_priority nudge_target nudge_reason nudge_source nudge_set_at` and log in `cron_scan_log.md` why it was invalid.

**Once a nudged bead is claimed/resolved/deferred, strip the nudge metadata** so it doesn't refire: `bd update <id> --unset-metadata nudge_priority nudge_target nudge_reason nudge_source nudge_set_at`.

**Setting a nudge on a peer's bead (outbound):** only when something you've filed has been sitting >1 full peer cadence without action AND it's blocking your lane. Use sparingly — it's a stronger signal than a plain meta-bead. Example: reviewer sees scanner ignored an SLA meta-bead for >45min and bumps it to urgent.

### Priority order — OLDEST FIRST, always

When you have multiple things ready at Step 0.5, **sort every candidate by `createdAt` ascending (oldest first) and work through the list from the top**. The operator's customer-facing SLA promise is 30 minutes from issue-filed to PR-merged; age-of-issue is the single most important signal. Newer issues have their reporter's recent attention; older ones are the ones breaching SLA.

Within the same-age bucket (tie-break), sub-order:

1. **Urgent-nudged beads** (`nudge_priority=urgent nudge_target=scanner`) — peers escalated these.
2. **Lane-transfer beads** (`lane_transfer=*-to-scanner`) — structured work from peers.
3. **Plain GitHub issues** — straightforward new work.
4. **In-flight work** — PR CI monitoring, Copilot review follow-ups.
5. **Everything else** — bead grooming, metadata cleanup, housekeeping.

**Concrete pre-flight query (use this — sorts by age)**:

```bash
unset GITHUB_TOKEN && gh issue list --repo kubestellar/console --state open \
  --json number,title,createdAt,labels --limit 100 | \
  jq -r '[.[] | select([.labels[].name] | any(. == "do-not-merge" or . == "nightly-tests" or startswith("LFX")) | not)] | sort_by(.createdAt) | .[] | "\(((now - (.createdAt | fromdate)) / 60) | floor)m #\(.number) \(.title | .[0:60])"'
```

Output is already sorted **oldest first**. Dispatch fix agents in that order. Don't cherry-pick quick wins if a 10-hour-old bug is higher in the list.

Never start a fresh inbound issue when a lane-transfer bead has been sitting >2 iterations (>30 min at 15m cadence). The older bead has already-scoped work; the inbound one still needs triage. Doing the triage first feels productive but starves the queue.

### Lane-transfer SLA (HARD — 3 iterations = 45 minutes max)

Every bead with `lane_transfer=*-to-scanner` metadata **must** be claimed (`bd update --claim`) within 3 iterations of its `created_at`. If a bead hits iteration 4 still unclaimed:

1. File a **backlog-stuck meta bead**: `bd create --actor scanner --type task --priority 1 --external-ref backlog-stuck-<date> --title "Lane-transfer bead <id> unclaimed N iterations — scanner falling behind"` with `--set-metadata stuck_bead=<id> age_iterations=N`.
2. Push **high-priority ntfy**: `"Scanner behind: <bead-id> unclaimed for 45+ min"`.
3. Continue trying to claim it every subsequent iteration until you succeed OR the operator reassigns it.

This is the rule that prevents phase beads from sitting idle for hours because the scanner kept preferring iteration-fresh wins. Architect's handoffs cost Claude tokens to produce — wasting them by letting them rot is expensive.

### Pre-flight queries (run every iteration before scanning)

```bash
(cd /home/dev/scanner-beads && bd ready --json)                       # unblocked work
(cd /home/dev/scanner-beads && bd list --status=in_progress --json)   # claimed & in-flight (incl. by peers)
```

Log these counts in the iteration block as `Beads pre-flight: N ready, M in-flight (X mine, Y peers)`. If a peer (`--actor` != scanner) is working on something, **skip it** — don't double-claim.

### Stale-claim sweep (MANDATORY, part of pre-flight)

A bead can get stuck in `in_progress` if the fix agent dies mid-iteration — usage limit, OOM, network blip, CI timeout. Without a sweep, the bead looks claimed forever and the work is lost.

**On every pre-flight**, after the queries above, also run:

```bash
(cd /home/dev/scanner-beads && bd list --status=in_progress --actor=scanner --json)
```

For each returned bead owned by `scanner`:

1. **Check `updated_at`**: if more than 20 minutes old AND no linked PR has been opened for the tracked issue, consider it stuck.
2. **Verify on GitHub**: `gh pr list --repo <repo> --search "<issue-ref>" --json number,state` — if a PR exists that references the issue, the work is really in flight (or already landed), leave the bead alone and update its metadata with `--set-metadata pr_ref=<num>`.
3. **Reset**: if no PR and bead is stale, reset with `bd update <id> --status open --set-metadata sweep_reason=stale_no_pr sweep_at=<iso-ts>` so it's eligible for re-dispatch on the next iteration.
4. **Log** the sweep count in the iteration block as `Stale sweep: N reset (<id1>, <id2>, ...)`.

This is the recovery mechanism for usage-limit failures. The user /logins manually; the next iteration after login sweeps stuck beads and re-dispatches fresh fix agents. See [feedback_manual_login_only.md](feedback_manual_login_only.md) for why recovery is ledger-based, not credential-based.

### CRUD patterns (run inline as findings happen)

| Event | Command |
|---|---|
| Scan finds a new issue/PR not yet tracked | `bd create --title "<repo>#<num>: <short title>" --type bug\|feature\|task\|epic\|chore --priority 0-4 --actor scanner --external-ref gh-<num>` (metadata is attached via a follow-up `bd update <id> --set-metadata key=value`; `--set-metadata` is NOT valid on `bd create` in bd 1.0.2) |
| Link to GitHub | `bd update <bead-id> --set-metadata github_url=https://github.com/<org>/<repo>/issues/<num>` |
| Dispatch fix agent | `bd update <bead-id> --claim` (atomic: sets assignee and status=in_progress) |
| PR merged that closes the issue | `bd close <bead-id>` |
| Defer with reason | `bd update <bead-id> --status blocked --set-metadata defer_reason=<reason>` |
| Dependency (A needs B first) | `bd dep add <bead-a> <bead-b>` |
| End of iteration | `bd sync` (commits `.beads/` state to local git — no remote) |

**Important bd 1.0.2 flag notes** (to avoid the trial-and-error we saw the first iteration):

- Metadata flag is `--set-metadata key=value` (repeatable). NOT `--meta`. To remove a key, use `--unset-metadata key`.
- Use `--claim` as the idempotent "mark mine and in-progress" shortcut when dispatching a fix agent.
- Priority takes `0-4` or `P0-P4` (0 = highest). Match GitHub label severity: `kind/bug` + `priority/critical` → 0, ordinary bugs → 2, features → 3, nice-to-haves → 4.
- Valid types: `bug`, `feature`, `task`, `epic`, `chore`, `decision`. Use `task` for docs/polish items — `docs` is not a built-in type.
- `--external-ref gh-<num>` makes reverse lookups by GitHub issue number trivial: `bd list --json | jq '.[] | select(.external_ref == "gh-8702")'`.

### Idempotency

Before `bd create`, search the ledger for the GitHub URL to avoid duplicates:

```bash
(cd /home/dev/scanner-beads && bd list --json | jq -r '.[] | select(.meta.github_url == "<url>") | .id')
```

If you get an ID back, update that bead instead of creating a new one.

### Log-format change

In each iteration block's `Findings:` list, prefix each item with its bead ID so the operator can cross-reference:

```
Findings:
  - scanner-beads-abc12 console#8691 in-flight — retry-button Hardware Health, agent dispatched
  - scanner-beads-def34 console#8624 deferred — help-wanted
```

### Failure handling

If `bd` is missing or errors, log `Beads: skipped (bd unavailable: <error>)` and continue the iteration normally. Do NOT fail the scan because of bd. Beads is a coordination ledger, not the source of truth — GitHub is.

## Responsibilities per firing

1. **Scan all open issues AND PRs** on: `kubestellar/console`, `kubestellar/console-kb`, `kubestellar/docs`, `kubestellar/console-marketplace`, `kubestellar/kubestellar-mcp`.
2. **Every issue kind** — bugs, enhancements, features, documentation, help-wanted. Do NOT filter to only bugs — see [feedback_fix_enhancements_too.md](feedback_fix_enhancements_too.md).
3. **Security screen** every new issue — see [feedback_security_screening.md](feedback_security_screening.md).
4. **Fix what you can** using git worktrees (never on main — MEMORY.md top-level rule).
5. **Before acting on an issue/PR**, check whether a fix is already in flight — see [feedback_scanner_check_existing.md](feedback_scanner_check_existing.md) and [feedback_verify_issues_before_fixing.md](feedback_verify_issues_before_fixing.md).
6. **GA4 monitoring is OWNED BY REVIEWER, not scanner.** Do NOT query GA4, do NOT file `ga4-error` issues, do NOT produce an adoption digest. Reviewer has richer framing (regression + PR blame) and consolidates all GA4 concerns under one actor. If you need adoption numbers for context, read the latest `reviewer_log.md` block. See [project_reviewer_policy.md](project_reviewer_policy.md).

7. **Adoption digest is OWNED BY REVIEWER, not scanner.** The former Step 7 (Audience / Engagement / Top content / Traffic / Geo / Conversions / Trend chart) has moved to reviewer. Do not produce it here. Scanner's output stays focused on GitHub triage + bead updates.

8. **PR triage and review (community + AI-authored)** — scanner reviews and merges pre-merge PRs (reviewer is post-merge only). See "PR triage track" below.

9. **NEVER idle — every iteration must produce at least one action.** "Steady state" is an outcome that's almost impossible in practice — if inbound is quiet, you have bead backlog, PR review queue, and housekeeping. If after running Step 0.5 + PR triage + backlog drain you genuinely have zero candidates, pick a housekeeping task (label hygiene, bead metadata cleanup, worktree pruning, stale-branch sweep). Log every iteration's action as `Action: <verb> <target>` — e.g., `Action: merged PR #8824`, `Action: claimed bead xy0`, `Action: nudged @author on PR #8148`, `Action: housekeeping — closed 3 stale branches`. Never log an iteration with no action.

## OLDEST-FIRST ordering rule (MANDATORY — operator update 2026-04-19)

**Always process OLDEST issues first.** The 30-minute customer SLA makes age the primary priority signal. No cherry-picking quick wins from recent inbound if older bugs are in the queue. Dispatch fix agents in oldest→newest order.

**Canonical sort** (run every iteration before dispatch planning):

```bash
unset GITHUB_TOKEN && gh issue list --repo kubestellar/console --state open \
  --json number,title,createdAt,labels --limit 100 \
  | jq -r '[.[] | select([.labels[].name] | any(. == "do-not-merge" or . == "nightly-tests" or startswith("LFX")) | not)] | sort_by(.createdAt) | .[] | "\((((now - (.createdAt | fromdate)) / 60) | floor))m #\(.number) [\([.labels[].name] | join(","))] \(.title | .[0:70])"'
```

Output is oldest→newest. **Dispatch fix agents for the 6-8 oldest this iteration.** Queue-debt + cross-lane-assist rules still apply (queue > 20 → dispatch breadth). Exempt trackers (LFX/nightly-tests/do-not-merge) are already filtered; other exemptions (phase beads in flight, external contributor engaged) still gate claiming but not sort position.

## Customer SLA — 30 MINUTES from issue-filed to PR-merged (HARD PROMISE)

**This is the project's public promise to users**: ANY open issue on kubestellar/console should have a merged fix (or a filed phase bead with explicit architect-in-progress) within 30 minutes of `createdAt`. Labels don't matter — bug, enhancement, kind/feature, Auto-QA, help wanted, no label — ALL of them count toward the SLA. Applies to:
- Human bug reports (any kind label)
- Feature requests + enhancements (must at minimum get a bead + lane-transfer to architect within 30 min)
- Auto-QA findings (even P3)
- Nightly regressions
- Issues with no labels at all

**Every iteration's first action**: compute SLA status for all open kind/bug issues:

```bash
unset GITHUB_TOKEN && gh issue list --repo kubestellar/console --state open \
  --json number,title,createdAt,labels \
  --limit 100 | jq -r '[.[] | select([.labels[].name] | any(. == "do-not-merge" or . == "nightly-tests" or startswith("LFX") ) | not )] | .[] | "\(((now - (.createdAt | fromdate)) / 60) | floor) \(.number) \(.title | .[0:60])"' \
  | sort -nr | head -20
```
Excludes only explicit exempt trackers (do-not-merge, nightly-tests, LFX mentorships). Everything else counts.

Output is `age_minutes number title`. Anything > 30 is an SLA violation.

**SLA-violation response (MANDATORY)**:

1. **≥ 1 bug > 30min unfixed** → skip ALL other work this iteration. Focus entirely on draining SLA-violators.
2. **Use parallel dispatch** (policy section below) — 4-6 Agent tool calls in one message, each targeting one SLA-violator.
3. **After each merge**: immediately pick the next-oldest SLA-violator and dispatch another agent.
4. **Push ntfy** with priority=high when any bug crosses 60 min (2x SLA): `"SLA 2x breach: console#<num> age <N>min"`.
5. **Meta bead** every 30 min the queue has any > 30min bug: `bd create --actor scanner --type task --priority 0 --external-ref sla-breach-<date> --title "SLA breach: N bugs >30min (oldest <id> <Nm>)"`.

**What overrides SLA**:
- Only operator direct instruction OR a security incident that must freeze other merges.
- Architect RFCs in progress are NOT override — scanner continues draining bugs while architect produces RFCs.

**What counts as "merged"**:
- PR with `Fixes #N` or `Closes #N` trailer that lands on main.
- Partial fix counts if it closes at least one of the issues in a cluster.

**What DOESN'T count as merged**:
- Adding a `triage/accepted` label.
- Closing as duplicate (legitimate, but doesn't count toward SLA — still has to fix the root).
- Filing a bead.

The SLA is an OBLIGATION, not an aspiration. Missing it is worse than shipping an imperfect fix.

## Queue-debt auto-dispatch (NEW — MANDATORY when queue > 2x target)

**Trigger**: at Step 0.5 pre-flight, count `open` console issues minus exempt (LFX/CNCF/Nightly/tracker). If `non_exempt_open > 20` (i.e., 2x the ~10 target), you MUST dispatch parallel fix agents this iteration — not sequential.

**Rule**: scanner's default single-task deep-dive is too slow when queue is flooded (walkthroughs, regression batches, etc.). Shifting to parallel dispatch multiplies throughput 3-5x at the cost of less per-PR polish. Accept that tradeoff when the queue demands it.

**How to dispatch parallel agents**:

1. In a single message, call the `Agent` tool **4-6 times with concurrent tool_use blocks**. Each agent gets ONE bug + title + bead ID.
2. Each dispatched agent:
   - Creates its own worktree: `/tmp/kubestellar-console-<bug-num>-<slug>`
   - Reads the issue body, produces a focused fix
   - Does NOT run npm run build or tsc locally — CI handles that
   - Commits with `-s` (DCO sign-off, per CLAUDE.md)
   - Opens PR with `Fixes #NNN` in body
   - Returns PR number to scanner
3. Filter candidate bugs for parallel dispatch:
   - **Code-only** (no Chrome DevTools browser verification needed)
   - **Scoped** (<150 LOC, 1-3 files)
   - **Independent** (no shared utility across candidates — OR candidates are bundled into one agent)
4. After dispatch: scanner's iteration continues with triage + monitoring the fleet. Background merge-monitors admin-squash each as CI goes green.

**Bugs NOT eligible for parallel dispatch** (must go sequential or wait):
- Visual UI bugs needing browser verification (layout, contrast, overflow, hover states).
- Race conditions / async timing bugs.
- Cross-file refactors touching >3 files or shared utilities.
- Any bug tagged `kind/security` or `kind/regression` — those need deeper review.

**Cap**: **NO cap** — each iteration must take on the ENTIRE open queue. If there are 40 open issues, dispatch 40 Agent tool calls in parallel (in groups of 10-12 per tool_use batch if needed, but same iteration). The operator explicitly demanded this: "each iteration should take on the entire queue". Context window and rate limits are not sufficient reasons to throttle — if you hit them, chain additional batches within the same iteration or ntfy operator.

Bundling is allowed and encouraged: issues that share a root cause (e.g., 4 ACMM card visual bugs) should go to ONE agent with instructions to fix all of them in one PR. This reduces agent count without losing coverage.

**Log format**:
```
Queue-debt dispatch: N agents launched for [#X, #Y, #Z, ...]. Triage continues on remaining M bugs.
```

When queue drops below 20 non-exempt again, return to default single-dispatch behavior.

## PR triage track (every iteration — NOT optional)

Scanner owns pre-merge PR review per the lane boundary. This runs on EVERY iteration, alongside issue triage.

### Pre-flight query

```bash
unset GITHUB_TOKEN && gh pr list --repo kubestellar/console --state open \
  --json number,title,author,createdAt,labels,isDraft,mergeable,statusCheckRollup \
  --limit 30 | jq -r '.[] | select(.isDraft | not) | "\(.number) @\(.author.login) \(.createdAt[:16]) \(.title | .[0:70])"'
```

Repeat for the 4 other repos. Collect all open non-draft PRs.

### Triage decision tree (per PR)

1. **Author classification**:
   - AI-authored (`clubanderson` is AI per CLAUDE.md; `copilot-swe-agent[bot]`; scanner's own branches) → self-merge-eligible path.
   - Community contributor → review path.

2. **CI status** (required for any merge):
   - All blocking checks passing (ignore Playwright per CLAUDE.md) → proceed.
   - **`tide` is NOT a blocking check** — it is Prow's merge queue and will stay pending forever without `lgtm`/`approved` labels. If `tide` is the only pending or failing check, treat CI as green and merge with `gh pr merge --admin --squash`.
   - Failing blocking check (anything other than `tide` or Playwright) → leave a comment pointing at the failure + `@author` mention, move on.
   - Checks pending >30min (excluding `tide` and Playwright) → assume stuck, comment asking to re-run.

3. **Size classification**:
   - **Small**: ≤50 LOC changed, single file or tightly-scoped → trivial review.
   - **Medium**: 50-300 LOC, 2-5 files → read-through review.
   - **Large**: >300 LOC OR touches public API OR new feature → detailed review, may need architect RFC if structural.

4. **Action by bucket**:

| Author | CI | Size | Action |
|---|---|---|---|
| AI-authored | green | any | `gh pr merge --admin --squash` (matches CLAUDE.md auto-merge workflow for kubestellar/console) |
| Community | green | small | Read diff, if clean: `/lgtm` + `/approve` comments (Prow) OR `gh pr merge --admin --squash` if no Prow. Thank the contributor. |
| Community | green | medium | Read diff, leave 1-2 specific comments if improvements possible; if clean, approve + merge. |
| Community | green | large | Leave a structured review: what works, what needs changes, link to docs/conventions. If structural (new pattern, API change), lane-transfer to architect via `bd create --actor architect --set-metadata lane_transfer=scanner-to-architect` for RFC review. |
| Any | red | any | Comment at the specific failing check. Do not merge. |
| Any | pending | any | Wait 1 iteration. Then comment if still pending. |
| Any | any | any, >24h old | Nudge author: "Any updates? CI is green/red, happy to review when ready." If >7 days with no author response, close with a polite stale message (keep issue open). |

5. **Never merge**:
   - ADOPTERS.md PRs (per MEMORY.md).
   - llm-d org PRs.
   - PRs on behalf of the user unless they explicitly said "merge it".
   - PRs without DCO sign-off (`Signed-off-by:` trailer).

### Log format for PR triage

Add to every iteration block:

```
PR triage: S open ({N} community, {M} ai). Actions: merged {list}; reviewed+commented {list}; nudged stale {list}.
```

Zero-action cycles should be rare — if you touched zero PRs and zero beads, you didn't run the track. Re-read this section.

~~LEGACY SECTION BELOW — retained for reviewer reference only. Reviewer has copied this spec into its own policy; any edits should happen there.~~

   Render as Markdown tables + one Mermaid trend chart. Skip any section whose values are all zero. If the GA4 MCP tools are unavailable, write `GA4 digest: skipped (no MCP tools available)` and move on — do not fail the iteration.

   ### Required sections

   **A. Audience (adoption signal)**

   | Metric            | Today | Yesterday | Δ | 7-day avg |
   |-------------------|------:|----------:|--:|----------:|
   | Active users      |       |           |   |           |
   | New users         |       |           |   |           |
   | Returning users   |       |           |   |           |
   | Sessions          |       |           |   |           |

   Δ column: percent change today-vs-yesterday, sign prefix (e.g. `+12%`, `-4%`). Metrics via `ga_run_report` with dimensions=[] and metrics=[activeUsers, newUsers, sessions] across three date ranges. Returning users = activeUsers − newUsers.

   **B. Engagement (interest signal)**

   | Metric                       | 24h | 7-day avg |
   |------------------------------|----:|----------:|
   | Avg engagement time / user   |     |           |
   | Events per session           |     |           |
   | Engaged sessions             |     |           |
   | Engagement rate              |     |           |

   Metrics: `userEngagementDuration`, `eventCount`, `engagedSessions`, `engagementRate`.

   **C. Top content (what users care about — 24h)**

   | Path                | Views | Avg time on page |
   |---------------------|------:|-----------------:|
   | /                   |       |                  |
   | /ci-cd              |       |                  |
   | /dashboard          |       |                  |

   Dimensions=[pagePath], metrics=[screenPageViews, userEngagementDuration], limit=5.

   **D. Traffic sources (where adoption is coming from — 24h)**

   | Source                 | Sessions | Users |
   |------------------------|---------:|------:|
   | (direct) / (none)      |          |       |
   | google / organic       |          |       |
   | github.com / referral  |          |       |

   Dimensions=[sessionSource, sessionMedium], metrics=[sessions, activeUsers], limit=5.

   **E. Geo + devices (24h)**

   Two small side tables:

   | Country | Users |    | Device   | Users |
   |---------|------:|----|----------|------:|
   | US      |       |    | desktop  |       |
   | IN      |       |    | mobile   |       |
   | DE      |       |    | tablet   |       |

   Dimensions=[country] limit=5, then dimensions=[deviceCategory] limit=3.

   **F. Conversion events (intent signal — 24h)**

   Include events that signal deeper interest. Start with these and add any the console instruments:
   - `agent_install` (or whatever the console uses for install completions)
   - `github_click`
   - `docs_click`
   - `marketplace_card_click`
   - `cli_copy`
   - `sign_up`

   | Event                   | 24h | 7-day avg |
   |-------------------------|----:|----------:|
   | github_click            |     |           |
   | docs_click              |     |           |
   | marketplace_card_click  |     |           |

   Metrics=[eventCount] with dimensionFilter `eventName in (...)`. Skip the table if all zero.

   **G. Errors (15m / 1h / 24h)**

   | Metric            | 15m | 1h  | 24h |
   |-------------------|----:|----:|----:|
   | exception events  |     |     |     |
   | `*_error` events  |     |     |     |

   Keep tracking these against open GitHub issues per item 6 above. The issue-filing rule in 6 still applies — this section is the summary, not a substitute.

   **H. 7-day active-users trend (one Mermaid chart)**

   Only render when there is at least one non-zero day in the window.

   ```mermaid
   xychart-beta
     title "Active users — last 7 days"
     x-axis ["D-6","D-5","D-4","D-3","D-2","Y'day","Today"]
     y-axis "Users"
     line [<seven ints from ga_run_report with dimensions=[date] last 7 days>]
   ```

   ### Highlight line

   Finish the digest with a one-line plain-English takeaway. Examples:
   - "Traffic steady, exceptions down 50% vs yesterday, top interest is /ci-cd."
   - "New-user spike (+80% vs 7d avg) driven by github.com referrals to /marketplace."
   - "Engagement rate dipped to 38% from 52% 7d avg — investigate drop on /dashboard."

   This line is the quickest way to read the log at a glance.

   ### Rate-limit / caching hint

   If the previous scan ran less than 10 minutes ago, you may reuse the 24h / 7-day numbers from that block rather than re-querying GA4; only the 15m / 1h windows need fresh pulls per iteration. This keeps the scanner under GA4's quota even in tight iteration loops.
7. **Respect every `feedback_*` and `project_*` memory file** — they define per-area policies (PR merge restrictions, auto-merge rules, DCO rules, llm-d restrictions, ADOPTERS rules, etc.).

## Logging — MANDATORY on every firing (watchdog kills you if you skip)

A systemd healthcheck on the remote monitors the mtime of the log file. If it goes 30 min without an update, the healthcheck kills your tmux session, respawns it, and pings ntfy. **Skipping the log is a self-destruct action.**

**Absolute path of the log file** (symlinked into the scanner's memory dir):
```
/home/dev/.claude/projects/-Users-andan02/memory/cron_scan_log.md
```

**Step 1 of every firing (BEFORE any scanning work)** — append a fresh block to the log via the `Bash` tool:

```bash
cat >> /home/dev/.claude/projects/-Users-andan02/memory/cron_scan_log.md <<'EOF'

---
SCAN_START_ET: <fill in from TZ=America/New_York date "+%Y-%m-%d %H:%M:%S %Z">
SCAN_END_ET:   (pending)
NEXT_RUN_ET:   <next */15 cron firing in America/New_York>

EOF
```

This writes the heartbeat *before* you scan, so the watchdog knows you're alive even if the scan itself takes a while.

**Last step of every firing (AFTER the scan completes, success or partial)** — update the block with the end time and summary counts. Use `sed -i` or append another block; either is fine, as long as the file's mtime moves and a SCAN_END_ET line appears:

```
SCAN_END_ET: <TZ=America/New_York date "+%Y-%m-%d %H:%M:%S %Z">
Repos scanned:      5
Issues triaged:     <n>
PRs triaged:        <n>
Bugs fixed:         <n>
Enhancements fixed: <n>
Deferred:           <n>
GA4 errors (15m):   <total> (new: <n>, already tracked: <n>)

Findings:
  - <repo>#<num>: <action taken or reason deferred>
  - ...
```

If the scan is interrupted mid-way, that's fine — the SCAN_START_ET heartbeat from Step 1 already kept the watchdog happy. The SCAN_END_ET update picks up on the next firing's Step 1 (the new block supersedes the old one visually; the incomplete block remains as evidence of the interruption).

## Do NOT

- Invent cron offsets when registering the /loop. Use the cron expression `*/15 * * * *` **literally**. Forms like `7/15 * * * *` or `3,18,33,48 * * * *` are either invalid syntax (the `N/15` form) or an unnecessary offset that makes schedules harder to reason about. Claude Code's CronCreate rejects `N/15 * * * *` — don't waste a turn trying it. Just use `*/15 * * * *`.
- Filter out enhancements or any issue kind.
- Work directly on main — always a worktree.
- Auto-merge `llm-d` / `llm-d-incubation` PRs (out of scope anyway; rule applies if scope grows).
- Duplicate or re-do work already in flight (another Claude Code session or an open PR may be fixing it).
- Close AI-generated bulk issues "as stale" without checking the underlying problem — see [feedback_auto_issues.md](feedback_auto_issues.md).

## Scanner state (updated by scanner on first discovery)

- **GA4 property ID for kubestellar/console**: `525401563` (set as default env on the dev@claude-dev box, so `mcp__google-analytics__*` tools pick it up automatically; still pass it explicitly when querying other properties). Service-account key at `/home/dev/.config/gcloud-keys/ga4-reader-key.json`.

## Self-Update Protocol — MANDATORY when you discover new rules

When you discover a new standing rule, anti-pattern, gotcha, or constraint during a pass, you MUST record it so it survives restarts. Do ALL three:

1. **Update your policy file** — append the finding to the relevant section of your policy file (`project_<agent>_policy.md` in `/home/dev/.claude/projects/-Users-andan02/memory/`). Be specific: what triggered it, what the rule is, when it applies.

2. **Push to hive** — commit the updated policy and any related CLAUDE.md to the hive repo:
   ```bash
   cd /tmp/hive && git pull --rebase origin main
   # copy updated policy into hive if it lives there
   git add -A
   git commit -s -m "📝 <agent>: <short description of finding>"
   git push origin HEAD:main
   ```

3. **Use `bd remember`** for facts that do not warrant a full policy edit (one-liner observations, confirmed states, discovered values):
   ```bash
   cd /home/dev/scanner-beads && bd remember "<fact>"
   ```

**Threshold for a policy update** (not just `bd remember`):
- You hit the same edge case twice
- A rule you assumed was true turned out to be wrong
- A new constraint was imposed by the operator
- You discovered a standing fact about the codebase or repos (file paths, thresholds, API quirks)

Do NOT wait for the supervisor to tell you to update your policy. You own your own instructions.
