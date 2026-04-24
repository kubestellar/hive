# hive

**One command starts everything. Your phone buzzes if anything needs you.**

---

```mermaid
flowchart LR
    conf["hive.conf<br>repos to watch<br>ntfy topic"] --> vel
    gh["GitHub"] --> vel

    subgraph gov["kick-governor  every 15 min"]
        vel["velocity<br>activity/hr  busy %"]
        mode["SURGE  BUSY<br>QUIET  IDLE"]
        vel --> mode
    end

    mode --> sc["scanner"]
    mode --> rv["reviewer"]
    mode --> ar["architect"]
    mode --> ot["outreach"]
    mode --> sp["supervisor"]

    sc --> bd[("beads<br>shared ledger")]
    rv --> bd
    ar --> bd
    ot --> bd
    sp --> bd

    sc --> ph["ntfy  phone"]
    rv --> ph
    ar --> ph
    ot --> ph
    sp --> ph
```

---

## Setup

```bash
# 1. install tmux
sudo apt install tmux

# 2. install hive
curl -fsSL https://raw.githubusercontent.com/kubestellar/hive/main/install.sh | sudo bash

# 3. configure
sudo nano /etc/supervised-agent/hive.conf
#   NTFY_TOPIC=your-ntfy-topic    # free at ntfy.sh
#   HIVE_REPOS="owner/repo ..."   # repos to watch

# 4. start
hive supervisor --copilot   # or --claude
```

That's it. `hive supervisor` installs missing tools, starts all agents, sets the kick cadence, and launches the supervisor. No tmux knowledge needed.

---

## Commands

```bash
hive supervisor --copilot   # start everything
hive supervisor --claude    # start with Claude Code instead

hive status                 # live dashboard
hive attach supervisor      # watch the supervisor  (Ctrl+B D to leave)
hive attach scanner         # watch any agent

hive kick all               # immediate kick to all agents
hive kick scanner           # kick one agent

hive logs governor          # tail governor decisions
hive logs scanner           # tail any agent's service log

hive stop all               # stop everything
```

---

## How it works

The **kick-governor** measures issue and PR velocity across your repos every 15 minutes and picks a mode:

| Mode | Trigger | Scanner | Reviewer | Architect | Outreach | Supervisor |
|------|---------|---------|---------|-----------|---------|-----------|
| SURGE | >8 activity/hr | 10 min | 10 min | 30 min | 30 min | 30 min |
| BUSY  | >3 activity/hr | 15 min | 15 min | 3 h    | 3 h    | 1 h    |
| QUIET | >0.5 activity/hr | 15 min | 30 min | 1 h   | 1 h    | 2 h    |
| IDLE  | ≤0.5 activity/hr | 30 min | 1 h   | paused | paused | 4 h    |

Cadences are tunable in `/etc/supervised-agent/governor.env` — no restart needed.

---

## Config

`/etc/supervised-agent/hive.conf` — the only file you need to edit:

```bash
# Repos to watch (space-separated)
HIVE_REPOS="owner/repo1 owner/repo2"

# ntfy.sh topic for phone alerts (free at ntfy.sh)
NTFY_TOPIC=your-secret-topic

# Which CLI to use for the supervisor session
SUPERVISOR_CLI=copilot   # or claude
```

---

## Troubleshooting

```bash
hive status                  # check what's running
hive logs governor           # why did it kick / not kick?
hive logs scanner            # what is scanner doing?
hive attach supervisor       # watch supervisor live
journalctl -u claude-scanner # raw service log
```

---

Apache 2.0  ·  [Architecture](docs/architecture.md)  ·  [KubeStellar example](examples/kubestellar/)
