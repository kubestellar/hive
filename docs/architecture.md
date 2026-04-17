# Architecture

## Four components, four failure modes

| # | Unit | Trigger | Catches |
|---|---|---|---|
| 1 | `supervised-agent.service` | Always running; internal poll every `AGENT_POLL_SEC` (default 10s) | Agent process crash, tmux session killed, TUI-ready detection for `/loop` prompt injection, auto-approval of a known sensitive-file prompt |
| 2 | `supervised-agent-renew.timer` | Every 6 days + 5 min after boot | Claude Code `/loop` cron auto-expires at 7 days — kills the session so the supervisor re-registers a fresh one |
| 3 | `supervised-agent-healthcheck.timer` | Every 20 min + 5 min after boot | Agent is "alive" but not making progress (auth loop, stuck prompt, model stuck thinking) — watches heartbeat-file mtime |
| 4 | ntfy push inside the healthcheck | On stall, on recovery, on escalation | Operator not watching the box — phone push |

## Reactions to each failure mode

```mermaid
sequenceDiagram
    autonumber
    participant S as supervisor
    participant T as tmux session
    participant A as agent
    participant L as heartbeat.log
    participant R as renew.timer
    participant H as healthcheck.timer
    participant N as ntfy.sh

    Note over S,T: boot-time / first run
    S->>T: new-session
    T->>A: spawn agent
    S->>T: wait for AGENT_READY_MARKER
    S->>A: send AGENT_LOOP_PROMPT
    A->>A: register /loop 15m cron

    loop every 15m (agent's own cron)
        A->>L: append SCAN_START_ET
        A->>A: do the work
        A->>L: append SCAN_END_ET + findings
    end

    Note over R: every 6d
    R->>T: kill-session
    S->>T: new-session (fresh /loop, new 7d TTL)

    Note over H: every 20m
    H->>L: stat mtime
    alt mtime fresh (age ≤ AGENT_STALE_MAX_SEC)
        H->>N: (if was stale) "recovered"
    else mtime stale
        H->>T: kill-session
        S->>T: new-session
        H->>N: "stalled, respawning (n/MAX)"
    end

    Note over H: after AGENT_MAX_RESPAWNS failed attempts
    H->>N: "manual intervention needed"
    H->>H: stop auto-respawning until recovery
```

## What this deliberately does NOT handle

- **Remote box offline / network partition.** If the whole machine is down, there's no process left to push a stall alert. A secondary watcher outside the box (uptimerobot, healthchecks.io, your laptop) is the correct answer, and is out of scope for this repo.
- **ntfy.sh downtime.** Free tier, rare, tolerable. Self-host or swap the transport if you need SLAs.
- **Agent logic bugs.** If the agent decides to do nothing forever but remembers to write the heartbeat, the healthcheck won't catch it. The log format in your policy file should include non-trivial counts (repos scanned, actions taken) so you can spot a "no-op loop" visually.
- **Secrets management.** Don't put credentials in `agent.env`. The agent should source them from its own credential store (`~/.claude/.credentials.json` for Claude Code, vault / secrets manager for anything else).
