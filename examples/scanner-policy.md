# Example: GitHub issue-scanner policy

This is the kind of markdown file the agent reads on every `/loop` firing when `AGENT_LOOP_PROMPT` is something like:

> `/loop 15m Follow every rule in scanner-policy.md from memory.`

Copy it into the agent's memory dir (for Claude Code: `~/.claude/projects/<slug>/memory/`) and adjust for your repos.

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
