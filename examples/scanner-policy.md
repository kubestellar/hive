# Example: GitHub issue-scanner policy

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

## Log format

Absolute path: whatever your `AGENT_LOG_FILE` env var is. Example for Claude Code memory:

```
/home/dev/.local/state/supervised-agent/heartbeat.log
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

## Do NOT

- Filter out enhancements or any non-bug issue kind.
- Work directly on `main`.
- Close bulk AI-generated issues "as stale" without checking the underlying problem.
- Skip the heartbeat — it's how the healthcheck knows you're alive.
- Skip Step 0 — operator edits to this file have to reach you via re-read, not via respawn.

---

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
