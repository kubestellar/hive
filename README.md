# hive

**One command starts everything. Your phone buzzes if anything needs you.**

---

![hive architecture](docs/hive-arch.svg)

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
|------|---------|---------|----------|-----------|----------|-----------|
| SURGE | queue > 20 | 10 min | 10 min | **paused** | **paused** | 30 min |
| BUSY  | queue > 10 | 15 min | 15 min | **paused** | **paused** | 30 min |
| QUIET | queue > 2  | 15 min | 30 min | 1 h        | 2 h        | 30 min |
| IDLE  | queue ≤ 2  | 30 min | 1 h    | 30 min     | 30 min     | 30 min |

Architect and outreach are **opportunistic** — they fill idle cycles and pause entirely under load. Supervisor runs every 30 min regardless of mode.

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
