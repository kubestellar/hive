# Example: GitHub scanner policy

This is the kind of markdown file the agent reads on every `/loop` firing when `AGENT_LOOP_PROMPT` is something like:

> `/loop 15m Follow every rule in scanner-policy.md from memory.`

Copy it into the agent's memory dir (for Claude Code: `~/.claude/projects/<slug>/memory/`) and adjust for your repos.

---

## Step 0 — pre-flight re-read (MANDATORY, before anything else)

> This step is the most important one. Copy it verbatim into any policy you write.

At the very start of every iteration, use the `Read` tool to re-fetch:

1. **This policy file** from disk.
2. Any companion files it references (other policy markdown, feedback notes, etc.).
3. The tail of the heartbeat/scan log (last ~100 lines) so you know what prior iterations did.

**Do NOT rely on in-context memory from previous iterations.** The agent runs in one long-lived session; its context may be days old. The operator edits policy files on their machine and Syncthing (or whatever sync mechanism) mirrors them into the agent's memory dir — the only way those edits take effect is if the agent re-reads them.

This step costs a few seconds each iteration and saves the operator from having to respawn the agent every time a policy rule changes.

If a file is missing or unreadable, log the failure to the heartbeat file under `Pre-flight: <file> read failed: <error>` and continue — don't abort the iteration.

---

## Responsibilities per firing

1. **Scan open issues AND PRs** on the configured repos.
2. **All issue kinds** — bugs, enhancements, docs, help-wanted. Do NOT filter to only bugs.
3. **Security-screen** every new issue before acting.
4. **Fix what you can** using git worktrees (never commit directly to `main`).
5. **Before acting on an issue or PR**, check whether a fix is already in flight (another agent, another PR, etc.).
6. **Log every iteration** — this is MANDATORY. If the heartbeat file goes more than `AGENT_STALE_MAX_SEC` seconds without an update, the healthcheck will kill your session and respawn you. Write the heartbeat *before* doing any work so interruptions still leave a trace.

## Field-tested patterns (learned the hard way — copy these)

> These emerged from a multi-hour live bake where a scanner managed a 50-issue walkthrough flood. Every rule below has a specific incident behind it.

### NEVER idle

"No new GitHub issues" ≠ "no work to do." When inbound is quiet, the ledger is often full of scanner-owned OPEN items waiting for action. **The string "Steady state" is ONLY valid when `bd ready` returns zero items AND no new issues arrived.** Otherwise your iteration outcome is "Backlog drain" and you must claim at least one bead before ending.

### Customer-SLA (30 min from issue-filed to PR-merged)

If your project publicly commits to a timely-response SLA, write it into the policy and make every iteration check age. Sort all open non-exempt issues by `createdAt` ascending and work through them oldest-first. Age beats priority — a 6-hour-old P3 should go before a 5-minute-old P1.

### Queue-debt auto-dispatch (parallel Agent tool calls)

When the open queue exceeds 2x your target, switch from serial single-task iterations to **parallel Agent dispatch**. Each iteration should launch 4-12 concurrent `Agent` tool calls (or equivalent subprocess fan-out), each targeting one bug or a bundled cluster. Background those dispatches and continue triage in the main thread. This multiplies throughput 3-5x at the cost of less per-PR polish — acceptable during floods.

Bundle issues that share a root cause (e.g., 5 i18n bugs in the same component) into a single agent call.

### Build-lock (OOM prevention)

Parallel dispatch is killed by parallel `npm run build` / `tsc --noEmit` processes. Each can consume 2-3 GiB RAM on a monorepo. **Every dispatched fix agent must acquire a flock before running heavy builds:**

```bash
flock /tmp/<project>-build.lock -c "cd /path/to/worktree && npm run build && npx tsc --noEmit"
```

This serializes builds across the dispatched fleet while keeping code-editing + PR-opening parallel. We learned this after 6 concurrent `tsc` processes pegged 32 GiB RAM + 4 GiB swap and required a container reboot.

### Cron cadence: continuous, not periodic

A 15-minute cron leaves the agent idle 90% of the time. If the queue is actively draining, `/loop 1m` keeps the agent firing continuously — moment one iteration ends, the next starts within a minute. Use `/loop 1m` for scanner, not `/loop 15m`.

### Cross-agent urgent-nudge protocol

Peer agents can escalate any bead to "urgent" for you using four metadata fields:

| Field | Value |
|---|---|
| `nudge_priority` | `urgent` |
| `nudge_target` | `scanner` (or `reviewer`/`feature`/`outreach`) |
| `nudge_reason` | short free-form text |
| `nudge_source` | actor that set the flag |
| `nudge_set_at` | ISO timestamp |

At Step 0.5 pre-flight, query for beads with `nudge_priority=urgent nudge_target=<your actor>` and act on them **before** normal priority order. Strip the metadata when done.

### Stale-cache supervisor gotcha

If your supervisor is a long-running bash daemon that caches its launch prompt in memory, patching the prompt on disk won't propagate until the daemon process is killed. When you change the `AGENT_LOOP_PROMPT`, `pkill` the supervisor process or restart its systemd service — don't just kill the tmux session and expect the supervisor to pick up the new prompt.

## Log format

Absolute path: whatever your `AGENT_LOG_FILE` env var is. Example for Claude Code memory:

```
/home/dev/.local/state/hive/heartbeat.log
```

Append one block per firing at the START of the iteration:

```
---
SCAN_START_ET: <America/New_York timestamp>
SCAN_END_ET:   (pending)
NEXT_RUN_ET:   <next firing in America/New_York>
```

Update the same block at the END with counts + findings:

```
SCAN_END_ET:   <timestamp>
Repos scanned:      5
Issues triaged:     <n>
PRs triaged:        <n>
Bugs fixed:         <n>
Enhancements fixed: <n>
Deferred:           <n>

Findings:
  - <repo>#<num>: <action taken or reason deferred>
```

## Target repos

Edit this list to match your project:

- `your-org/repo-a`
- `your-org/repo-b`
- `your-org/repo-c`

---

## Open-issue queue targets (healthy steady state)

Pick target counts per repo. Scanner reports against them every iteration as `Queue: <repo>=N (target N)` in its log block, and flags when a repo exceeds its target by >2.

| Repo | Target | Why |
|---|---:|---|
| your-primary-repo | ~10 | room for active work + tracker issues |
| your-secondary-repos | 0 | no intentionally-open items |
| your-exempt-repos (community-card or outreach repos) | exempt | intentional long-lived stubs |

**Don't force-close to hit the number.** Close reasons must be legitimate (fixed / duplicated / invalid / stale-no-reporter-response). The target is a health signal, not a quota.

---

## The "no PR = work on it" rule (the main queue-reduction lever)

If a GitHub issue is open AND has no linked PR (in flight or merged), **scanner owns driving it forward** — regardless of `help wanted` / `kind/feature` / `enhancement` labels. Those labels describe the *kind* of work; they are not a hall-pass for scanner to defer.

Sequence for every unPR'd issue:

1. **Does it need architecture first?** Cross-cutting pattern, fundamental decision (algorithm / storage / protocol), touches >3 files or any public API → file `--actor feature --set-metadata lane_transfer=scanner-to-architect` and continue. Feature will RFC, scanner implements the phase beads later.
2. **Is an external contributor engaged?** Check for: assignee set, recent non-maintainer comment in last 14d, a fork visible, or a draft/WIP PR referencing the issue. If yes → leave it, record `contributor_engaged=<login>`, nudge in 14 days if it's gone quiet.
3. **Is it an intentional tracker?** Keep an exempt list (mentorship trackers, CI-aggregator issues, umbrella trackers). Skip those.
4. **Otherwise → claim it.** Bundle small related issues into one PR when possible. Large single issues → one fix agent, one PR.

The rule applies equally to bugs, features, enhancements, docs. The only defer signals are the three exemptions. Silent queue backlog is a scanner bug, not a feature.

### Priority order — lane-transfer beads first, by age

When Step 0.5 returns multiple things ready, claim in this order:

1. **Lane-transfer beads** (`lane_transfer=*-to-scanner`) sorted by `created_at` ascending — oldest first. Peers handed you structured work; ignoring it rots.
2. **New GitHub issues** discovered this iteration.
3. **In-flight work** (PR CI monitoring, review follow-ups).
4. **Everything else.**

Never start a fresh inbound issue when a lane-transfer bead has been sitting >2 iterations. The older bead has already-scoped work; the inbound one still needs triage. Doing triage first feels productive but starves the queue.

### Lane-transfer SLA (HARD — 3 iterations max)

Every `lane_transfer=*-to-scanner` bead **must** be claimed within 3 iterations of its `created_at`. If a bead hits iteration 4 still unclaimed:

1. File a backlog-stuck meta bead with P1 priority and `--set-metadata stuck_bead=<id> age_iterations=N`.
2. Push high-priority ntfy: *"Scanner behind: <bead> unclaimed for <N> iterations."*
3. Keep trying to claim each subsequent iteration.

This prevents phase beads from sitting idle for hours because scanner preferred iteration-fresh wins. Architect's handoffs cost tokens to produce — letting them rot is expensive.

### Zombie-label rule — auto-dispatch labels are NOT defer signals

Many projects set labels like `ai-processing` / `ai-fix-requested` / `in-progress` when an auto-dispatcher triggers, to prevent double-claiming. But the dispatch can fail silently (usage limit, crash, timeout), leaving the label as a zombie marker on an issue where nothing is actually happening.

**Scanner's rule**: only a linked open/merged PR counts as "in progress." A label alone does NOT. When you find an issue with `ai-processing` / similar but no linked PR and no bead in your ledger, treat it as unPR'd and apply the sequence above. Strip or update the zombie label when you claim, so the label state reflects reality.

This rule exists because a single near-miss can be expensive — a cluster of 3 notification-UX issues on a real operator's project sat idle for 5+ hours under zombie labels before the operator noticed and asked why. If scanner had applied this rule rigorously, the cluster would have been bundled and dispatched within the first 15-minute iteration after the issues landed.

---

## Lane boundary (only matters with multi-agent setups)

If you're running scanner alongside reviewer / feature / outreach agents, make the ownership split explicit in each policy. Example split used on kubestellar/console:

| Lane | Owner |
|---|---|
| Inbound GitHub triage (issues, PRs, Copilot reviews) | scanner |
| Post-merge state + CI health + regressions | reviewer |
| Architecture RFCs + feature proposals + portfolio | feature |
| CNCF / community / adopters / docs-debt | outreach |

When scanner finds something in another lane (e.g., a broken CI workflow on main), file on that owner's behalf with `--actor <peer> --set-metadata lane_transfer=scanner-to-<peer>` — don't handle it yourself. See [`reviewer-policy.md`](reviewer-policy.md) for the mirror rule.

## Do NOT

- Filter out enhancements or any non-bug issue kind.
- Work directly on `main`.
- Close bulk AI-generated issues "as stale" without checking the underlying problem.
- Skip the heartbeat — it's how the healthcheck knows you're alive.
- Skip Step 0 — operator edits to this file have to reach you via re-read, not via respawn.

---

## Optional: shared work ledger via `beads`

If you'll run more than one agent against the same project, or you want structured "what's still in flight from prior iterations" state, wire up [beads](https://github.com/steveyegge/beads) (`bd` CLI) as an internal ledger. beads is a git-backed Dolt DB that stores issues, dependencies, and status; agents coordinate by reading/writing the ledger with distinct `--actor` names, which prevents double-claimed work.

**Install** (release binary, Linux amd64 example):

```sh
cd /tmp && curl -sSLO https://github.com/steveyegge/beads/releases/latest/download/beads_linux_amd64.tar.gz
tar xzf beads_linux_amd64.tar.gz && sudo install -m 0755 bd /usr/local/bin/bd
```

**Init** (as the agent user):

```sh
mkdir ~/agent-ledger && cd ~/agent-ledger
git init -b main                     # bd sync needs a git dir (local-only, no remote required)
git config user.name "scanner"       # becomes default actor
bd init
```

**In your policy, add a Step 0.5** that runs:

- **Pre-flight**: `bd ready --json` and `bd list --status=in_progress --json` — peer agents surface as items with `actor != me`; skip them.
- **On every new finding**: `bd create --title "..." --type <bug|feature|task|epic|chore> --priority <0-4> --actor <agent-name> --external-ref <stable-key>` (after first checking `bd list --json | jq '.[] | select(.external_ref == "<key>")'` to avoid duplicates). Valid types are `bug|feature|task|epic|chore|decision` (not `docs` — use `task`). Priority is `0-4` (0 = highest) or `P0-P4`.
- **Attach metadata**: `bd update <id> --set-metadata key=value` (repeatable). Note the flag is `--set-metadata`, NOT `--meta`.
- **On dispatch**: `bd update <id> --claim` (idempotent; sets assignee=you and status=in_progress atomically).
- **On close**: `bd close <id>`.
- **End of iteration**: `bd sync` commits ledger state to local git.

**Multi-agent tips**:

- Each agent sets a distinct `--actor` (`scanner`, `reviewer`, `ideator`, etc.). Pattern: `bd ready --json | jq '[.[] | select(.owner_actor != "<me>" or .owner_actor == null)]'` to see "work I could claim".
- Same-host agents share the ledger directory; Dolt is file-locked and short-lived bd calls serialize cleanly.
- Cross-host agents: sync the ledger directory with Syncthing; handle rare conflicts by picking the newest bead and closing the stale one.
- Never push ledger state back to GitHub (no comments, no labels). The ledger is internal coordination; GitHub is still the source of truth for issues/PRs.

**Revert**: remove the Step 0.5 section from your policy — next iteration stops calling `bd`. `rm -rf ~/agent-ledger` if you want to wipe the history.

## Optional: adoption / site-health digest

If your agent has access to an analytics source (Google Analytics, Plausible, self-hosted metrics), consider appending a short digest to the heartbeat log each iteration alongside the issue findings. The pattern isn't just "did anything break" — it's a running pulse of *who's using the thing, what they care about, and whether engagement is trending up or down*.

Good sections to include:

- **Audience**: active / new / returning users — today vs yesterday, with delta %.
- **Engagement**: avg time per user, events per session, engagement rate.
- **Top content** (24h): top 5 pages by views.
- **Traffic sources** (24h): direct vs organic vs referrer breakdown.
- **Conversions** (24h): whatever the project instruments as intent signals.
- **Errors** (15m / 1h / 24h).
- **Trend chart** (Mermaid xychart-beta works well): 7-day active users or similar.
- **One-line English takeaway** at the bottom — fastest way to read the log at a glance.

Skip any section whose values are all zero so the log doesn't get noisy on a quiet day.
