# hive

**One command starts everything. Your phone, Slack, or Discord buzzes if anything needs you.**

![hive dashboard](docs/dashboard-screenshot.png)

---

![hive architecture](docs/hive-arch.svg)

---

## What is hive?

Hive is an autonomous multi-agent CI/CD companion that watches your GitHub repos, triages issues and PRs, merges what's ready, and escalates what isn't. It runs 7+ specialized agents (scanner, ci-maintainer, quality, architect, supervisor, outreach, guide) coordinated by an adaptive governor that scales effort up and down based on queue depth. The v2 branch packages everything into a single Docker container — one Go binary orchestrates agent tmux sessions, the governor loop, a real-time dashboard, and structured logging with rolling file retention.

The core idea: **if a human would give the same answer every time, it belongs in infrastructure, not in a prompt.** A deterministic pipeline of shell scripts handles filtering, classification, merge-gating, and enforcement *before* any LLM agent sees the work. Agents only handle judgment calls — reading code, reasoning about fixes, writing PRs.

---

## Hive Hub

The [Hive Hub](https://hive.kubestellar.io) is the central registry for all hives. It provides:

- **Public registry** — browse all registered hives, see agent counts, actionable issues, ACMM levels
- **Cross-hive leaderboard** — contributor rankings across all hives
- **Hosted hives** — spin up a cloud-hosted hive for your GitHub org/repo with OAuth-protected dashboard
- **Contribute** — donate your CLI + API tokens to help any hive's agent swarm

## Contribute to a Hive

Community members can contribute compute to any hive by running a CLI agent that connects to the hive's work queue.

### Quick start

```bash
brew install just gh
git clone -b v2 https://github.com/kubestellar/hive && cd hive
just contribute-setup claude    # picks your CLI, selects a hive
just contribute-hive            # starts contributing (Docker)
```

If `HIVE_HUB` is not set, you'll be shown a list of hives you have access to and can select one interactively.

### Supported CLIs

| CLI | Containerized | Host (local) | Model flag |
|-----|:---:|:---:|---|
| **Claude Code** | yes | yes | `--model` or `AGENT_MODEL` |
| **GitHub Copilot** | yes | yes | `--model` or `AGENT_MODEL` |
| **Pi** (pi.dev) | yes | yes | `--model` or `AGENT_MODEL` |
| **Goose** (Block) | yes* | yes | `GOOSE_PROVIDER` + `GOOSE_MODEL` |
| **Bob** (IBM) | no | yes | — |
| **Other** | no | yes | varies |

*Goose containerized requires ollama running on the host with `--network host`.

### Modes

- **Containerized (default)**: `just contribute-hive` — runs in Docker, all CLIs pre-installed
- **Host (non-containerized)**: `just contribute-hive <cli> local` — runs natively with tmux + relay

### Model selection

```bash
export AGENT_MODEL=claude-sonnet-4-6    # for claude, copilot, pi
just contribute-hive claude local

export GOOSE_PROVIDER=ollama GOOSE_MODEL=deepseek-r1   # for goose
just contribute-hive goose local
```

### How it works

1. `contribute-setup` authenticates with GitHub, registers with the hive, and verifies your CLI
2. `contribute-hive` starts the CLI in a tmux session and a WebSocket relay that connects to the hive
3. The hive assigns tasks (GitHub issues) — the relay injects prompts into the CLI session
4. When the agent completes a task, the relay reports back and gets the next one
5. Your credentials never leave your machine — the hive provides scoped GitHub tokens for repo access

### Trust tiers

Contributors start as **newcomer** (rate-limited) and auto-promote based on completed tasks:

| Tier | Tasks/hr | Tasks/day | Auto-promote at |
|------|:---:|:---:|---|
| Newcomer | 3 | 10 | — |
| Contributor | 10 | 50 | 5 completed |
| Trusted | 30 | 200 | 20 completed + vouched |

Rate limits and tiers are configurable by the hive owner in the governor config.

---

## Setup (Docker — recommended)

The v2 container packages everything: the Go orchestrator, dashboard, all CLI backends (Claude Code, Copilot, Gemini, Goose), tmux sessions, and a ttyd web terminal. One container, one config file, one volume.

### 1. Create config and data directory

```bash
mkdir -p /opt/hive/secrets
```

Copy the example config and edit it for your project:

```bash
curl -sL https://raw.githubusercontent.com/kubestellar/hive/v2/v2/deploy/hive.yaml \
  -o /opt/hive/hive.yaml
```

Edit `/opt/hive/hive.yaml` — set your org, repos, agents, and governor thresholds. See `v2/deploy/hive.yaml` for a full production reference.

### 2. Run the container

```bash
docker run -d \
  --name hive \
  --restart unless-stopped \
  -p 3001:3001 \
  -v hive-data:/data \
  -v /opt/hive/hive.yaml:/etc/hive/hive.yaml \
  -v /opt/hive/secrets:/secrets:ro \
  -e HIVE_GITHUB_TOKEN=ghp_... \
  ghcr.io/kubestellar/hive:v2-latest
```

The dashboard is at `http://<host>:3001`.

#### Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `HIVE_GITHUB_TOKEN` | Yes (unless using GitHub App) | Personal access token or fine-grained token |
| `HIVE_LEVEL` | No | ACMM level override (1–6) |
| `HIVE_REPO` | No | Override the primary repo from config |
| `ANTHROPIC_API_KEY` | For Claude backend | Anthropic API key |
| `TZ` | No | Timezone (default: `America/New_York`) |

#### GitHub App auth (recommended for production)

Instead of a personal token, use a GitHub App for isolated rate limits:

```bash
docker run -d \
  --name hive \
  --restart unless-stopped \
  -p 3001:3001 \
  -v hive-data:/data \
  -v /opt/hive/hive.yaml:/etc/hive/hive.yaml \
  -v /path/to/gh-app-key.pem:/etc/hive/gh-app-key.pem:ro \
  ghcr.io/kubestellar/hive:v2-latest
```

Set the `github_app` section in `hive.yaml`:

```yaml
github_app:
  app_id: 12345
  installation_id: 67890
  private_key_file: /etc/hive/gh-app-key.pem
```

### 3. Auto-update with Watchtower

Every push to the `v2` branch builds and publishes a new `ghcr.io/kubestellar/hive:v2-latest` image. Use [Watchtower](https://containrrr.dev/watchtower/) to automatically pull and restart the container when a new image is available:

```bash
docker run -d \
  --name watchtower \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  containrrr/watchtower \
  --cleanup --interval 60 hive
```

This checks for a new `hive` image every 60 seconds, pulls it, recreates the container with the same configuration, and cleans up the old image. Merge a PR to `v2` and the change is live within ~2 minutes (CI build + Watchtower poll).

### 4. Ports and volumes

| Port | Purpose |
|------|---------|
| 3001 | Dashboard (public, supports auth token) |
| 3002 | Internal API (not exposed by default) |
| 7681 | ttyd web terminal (not exposed by default) |

| Volume / Mount | Purpose |
|----------------|---------|
| `/data` | Persistent state: metrics, beads, logs, agent session data |
| `/etc/hive/hive.yaml` | Configuration (bind mount) |
| `/secrets` | GitHub App key, other secrets (read-only bind mount) |

### Legacy setup (systemd)

For bare-metal installs without Docker, the v1 systemd approach is still supported on the `main` branch:

```bash
curl -fsSL https://raw.githubusercontent.com/kubestellar/hive/main/install.sh | sudo bash
sudo cp config/hive-project.yaml.example /etc/hive/hive-project.yaml
hive supervisor
```

---

## Commands

```bash
hive supervisor             # start everything
hive status                 # live terminal dashboard (cached repo data)
hive status --repos         # refresh repo issue/PR counts from GitHub API
hive status --json          # machine-readable JSON output
hive status --json --repos  # JSON with fresh repo data (used by dashboard slow path)
hive status --watch 5       # auto-refresh every 5 seconds (in-place overwrite)
hive dashboard              # launch web dashboard (port 3001)
hive attach supervisor      # watch the supervisor  (Ctrl+B D to leave)
hive attach scanner         # watch any agent

hive kick all               # immediate kick to all agents
hive kick scanner           # kick one agent

hive switch scanner claude  # switch CLI backend (pins it)
hive switch reviewer copilot
hive unpin scanner          # let governor manage CLI again

hive model scanner claude-opus-4-6  # switch model for an agent

hive logs governor          # tail governor decisions
hive logs scanner           # tail any agent's service log

hive stop all               # stop everything
```

---

## Agents

| Agent | Role | Default cadence (idle) |
|-------|------|----------------------|
| **scanner** | Triages issues, fixes bugs, opens PRs, merges merge-eligible PRs | 15 min |
| **reviewer** | Code review, health check monitoring, nightly quality checks | 15 min |
| **architect** | Structural changes, refactors, API surface design | 30 min |
| **outreach** | Directory submissions, coverage tracking, community engagement | 2 h |
| **supervisor** | Monitors agent health, bead ledger, sweeps for rotting issues | 30 min |

Each agent runs in its own tmux session with a dedicated CLAUDE.md policy file. Agent policies support template variables (`${PROJECT_ORG}`, `${PROJECT_PRIMARY_REPO}`, etc.) that are substituted at kick time from `hive-project.yaml`.

---

## Governor

The **kick-governor** measures the issue queue across your repos and picks a mode:

| Mode | Trigger | Scanner | Reviewer | Architect | Outreach | Supervisor |
|------|---------|---------|----------|-----------|----------|-----------|
| **SURGE** | issues > 20 | 15 min | paused | paused | paused | 5 min |
| **BUSY** | issues > 10 | 15 min | 1 h | paused | paused | 10 min |
| **QUIET** | issues > 2 | 15 min | 45 min | paused | paused | 15 min |
| **IDLE** | issues ≤ 2 | 15 min | 15 min | 30 min | 2 h | 30 min |

Architect and outreach are **opportunistic** — they activate in idle mode and pause entirely under load.

The governor also manages **model selection** per mode. Priority agents (scanner, reviewer) get metered Claude backends in surge/busy. Non-priority agents use Copilot (free tier). Budget tracking projects weekly token spend and throttles to a safety threshold.

All cadences, thresholds, and model assignments are tunable in `/etc/hive/governor.env` — no restart needed.

### Exempt labels

Issues with these labels are excluded from the actionable queue and don't affect mode thresholds:

`hold`, `do-not-merge`, `nightly-tests`, `LFX*`, `auto-qa-tuning-report`, `meta-tracker`, `adopters`, `changes-requested`, `waiting-on-author`

---

## Deterministic Pipeline

Hive separates work into two layers:

- **Deterministic layer** (shell scripts + JSON + config) — handles every decision where a human would give the same answer every time. Runs before agents wake up.
- **Non-deterministic layer** (LLM agents) — receives pre-computed data and focuses on judgment calls: reading code, reasoning about fixes, writing PRs.

LLMs treat "NEVER" rules as suggestions. No amount of prompt engineering reliably prevents an agent from closing a hold-labeled issue or merging an untested PR. The deterministic pipeline removes those decisions from the agent entirely.

### Pipeline stages

Each stage runs as a shell script, declared in `hive-project.yaml`, with explicit dependencies:

| Phase | Stage | What it does |
|-------|-------|-------------|
| **Enumerator** | `enumerate-actionable.sh` | Queries GitHub, excludes hold/exempt labels, filters drafts, outputs canonical work list |
| **Classifier** | `issue-classifier.sh` | Assigns complexity, model tier, and lane based on label/title patterns |
| **Gate** | `merge-gate.sh` | Checks CI status, excludes drafts, validates author — outputs merge-eligible PRs |
| **Monitor** | `conflict-sweeper.sh` | Detects merge conflicts across open PRs |
| **Monitor** | `pr-cluster-detector.sh` | Groups related PRs by file overlap |
| **Monitor** | `architecture-detector.sh` | Identifies architecture-scope multi-directory refactors |
| **Monitor** | `copilot-comment-checker.sh` | Finds unaddressed Copilot review comments (high/medium/low severity) |
| **Monitor** | `ga4-anomaly-detector.sh` | Detects GA4 property anomalies |
| **Monitor** | `outreach-tracker.sh` | Tracks outreach-labeled PRs and GA4 metrics |
| **Enforcer** | `gh-wrapper.sh` | Wraps all `gh` calls — prevents merging to main, closing hold issues, pushing to protected branches |

Stages declare their consumers and dependencies. The pipeline runner (`run-pipeline.sh`) resolves the DAG and executes in parallel where possible.

### Adding a pipeline stage

1. Add an entry to `pipeline.stages[]` in `hive-project.yaml`
2. Write the script in `bin/`
3. Declare `output`, `consumers`, `phase`, and `depends`
4. The pipeline runner picks it up on the next kick cycle

### Config-driven rules

Classification patterns, clustering signals, severity keywords, and exempt labels all live in `hive-project.yaml`. Scripts read rules from config — they don't contain project-specific logic. Change the config, change the behavior.

---

## Web Dashboard

`hive dashboard` launches a real-time web dashboard on port 3001.

**Live monitoring:**
- Agent states, governor mode, repo counts, and beads refresh every 5 seconds via SSE
- Per-agent sparkline history (busy time, restart counts, rolling 24-hour window)
- Restart tracking with color-coded thresholds (yellow > 0, red > 5)
- Intensity gauge comparing recent vs trailing token rates
- Coverage tracking toward configured target

**Agent controls:**
- One-click kick for any agent
- CLI backend switcher dropdown (auto-pins to prevent governor override)
- Model switcher
- Pause / Resume (soft pause keeps session alive, operator pause persists until explicit unpause)
- Pin / Unpin (lock CLI or model to prevent governor changes)
- Restart counter reset

**Infrastructure health:**
- CI pass rate, nightly/weekly workflow status
- Homebrew formula freshness, Helm chart presence
- Release freshness (nightly within 24h, stable within 7 days)
- Deploy job status from CI workflow
- GitHub API rate limit monitoring with alerts
- GitHub App token status

**Configuration dialog:**
- Gear icon on every agent card and the governor block
- Agent config: general settings, cadences, models, pipeline toggles, hooks, restrictions, current prompt
- Governor config: thresholds, exempt labels, budget, notifications, health settings, agent CRUD

**Other features:**
- Token budget tracking with issue cost breakdown and model advisor
- Agent action summaries extracted from logs
- Live tmux pane capture per agent
- macOS Ubersicht widget (download from header button)

The dashboard runs as a systemd service (`hive-dashboard.service`) and auto-restarts on failure.

```bash
open http://<hive-ip>:3001        # from LAN (Docker: -p 3001:3001)
open http://localhost:3001        # from hive itself

# Optional: secure with auth token
docker run -d ... -e HIVE_AUTH_TOKEN=your-secret ... ghcr.io/kubestellar/hive:v2-latest
# Then access: http://<hive-ip>:3001?token=your-secret

# Install Ubersicht widget (macOS)
curl -sf http://<hive-ip>:3001/api/widget \
  | tar xzf - -C "$HOME/Library/Application Support/Übersicht/widgets/"
```

---

## Backends

Set `HIVE_BACKENDS` in `hive.conf`. `HIVE_AUTO_INSTALL=true` installs missing backends on startup.

| Backend | Type | Description |
|---------|------|-------------|
| `claude` | Native | Anthropic's CLI — runs Claude models directly (metered) |
| `gemini` | Native | Google's CLI — runs Gemini models directly |
| `copilot` | Aggregate | GitHub Copilot — routes to Claude, GPT, Gemini via GitHub's free tier |
| `goose` | Aggregate | Block's Goose — routes to any model via config: qwen, deepseek, llama, and more |

**Native backends** are single-vendor tools. **Aggregate backends** are multi-vendor routers that can call models from different providers through a single interface.

### Model selection per mode

The governor assigns backend:model pairs based on the current mode:

| Mode | Scanner | Reviewer | Architect | Outreach | Supervisor |
|------|---------|----------|-----------|----------|-----------|
| SURGE | claude:sonnet | claude:sonnet | claude:opus | copilot:opus | claude:haiku |
| BUSY | claude:sonnet | claude:sonnet | copilot:sonnet | copilot:sonnet | claude:haiku |
| QUIET | claude:haiku | copilot:sonnet | copilot:opus | copilot:sonnet | claude:haiku |
| IDLE | copilot:sonnet | copilot:sonnet | copilot:opus | copilot:sonnet | copilot:sonnet |

Priority agents get metered Claude under load for reliability. Everything else falls back to Copilot's free tier. When rate-limited, agents automatically fall back to the alternate backend with a 30-minute cooldown.

### Local models (optional)

Set `HIVE_MODEL_SERVICES="ollama litellm"` to run models on-device with no API costs.

```
ollama        → runs local models (llama3, codestral, qwen2.5-coder, ...)
    └── litellm proxy :4000  ← unified OpenAI-compatible endpoint
            └── goose        ← points here when AGENT_BACKEND=goose
```

Ollama and litellm start as background services before any agent session launches.

---

## GitHub App Integration

Hive can use a GitHub App for API access, isolating agent rate limits from your personal token:

```yaml
# in hive-project.yaml
github_app:
  app_id: 12345
  installation_id: 67890
  private_key_file: /etc/hive/gh-app-key.pem
```

The token generator (`gh-app-token.sh`) creates 1-hour installation tokens, caches them in `/var/run/hive-metrics/gh-app-token.cache`, and auto-refreshes 5 minutes before expiry. All agent API calls and dashboard health checks use the app token.

---

## Auto-Deploy

### Docker (v2) — Watchtower

Every push to the `v2` branch triggers a CI build that publishes a multi-arch Docker image (`linux/amd64` + `linux/arm64`) to `ghcr.io/kubestellar/hive:v2-latest`. With Watchtower running alongside the hive container (see [Setup](#3-auto-update-with-watchtower)), the deploy cycle is:

1. Merge PR to `v2`
2. GitHub Actions builds and pushes the new image (~90 seconds)
3. Watchtower detects the new digest and recreates the container (~60 seconds)
4. Total: PR merge to live in ~2 minutes, zero SSH

### Systemd (v1) — git pull timer

For bare-metal installs, `hive-deploy.timer` runs every 60 seconds:

1. `git pull --rebase origin main`
2. Syncs changed scripts from `bin/` to `/usr/local/bin/`
3. Syncs `hive-project.yaml` to `/etc/hive/`
4. Syncs systemd units and triggers `daemon-reload`
5. Restarts dashboard and Discord bot if their files changed
6. Drift-checks even if HEAD unchanged (catches manual edits)

---

## Discord Bot

`discord/bot.js` bridges the dashboard to a Discord channel:

- Agent transition announcements (working → idle, mode changes)
- Pipeline result posts
- Command routing (configurable prefix, default `!`)
- 15-minute heartbeat status embeds
- Rate-limited message queue (1.2s per message)

Configure in `hive-project.yaml`:

```yaml
discord:
  enabled: true
  bot_token_env: DISCORD_BOT_TOKEN
  channels:
    primary: "channel-id"
    alerts: "alerts-channel-id"
```

---

## Notifications

Hive sends alerts to any combination of ntfy, Slack, and Discord. Set whichever you use in `hive.conf` — all three fire simultaneously if configured.

| Channel | Config key | How to get it |
|---------|-----------|---------------|
| ntfy (phone push) | `NTFY_TOPIC` | Free at [ntfy.sh](https://ntfy.sh) — pick any topic string |
| Slack | `SLACK_WEBHOOK` | api.slack.com/apps → Incoming Webhooks |
| Discord | `DISCORD_WEBHOOK` | Channel Settings → Integrations → Webhooks |

---

## Adapting for Your Project

Hive is designed to be forked and configured, not hardcoded. All project-specific values live in `hive-project.yaml`.

### Step by step

1. **Copy the example config:**
   ```bash
   sudo cp config/hive-project.yaml.example /etc/hive/hive-project.yaml
   ```

2. **Edit the `project` section** — your org, repos, AI author account:
   ```yaml
   project:
     name: "My Project"
     org: "my-org"
     primary_repo: "my-org/my-repo"
     repos:
       - my-org/my-repo
       - my-org/my-docs
     ai_author: "my-bot-account"
   ```

3. **Edit `agents.enabled`** — pick which agents you need:
   ```yaml
   agents:
     enabled:
       - supervisor
       - scanner
       - reviewer
       # - architect    # optional
       # - outreach     # optional
       # - docs-agent   # add your own
   ```

4. **Edit `classification`** — your labels, lane patterns, complexity rules:
   ```yaml
   classification:
     complexity:
       simple:
         labels: ["typo", "docs"]
         model: "haiku"
       complex:
         labels: ["architecture", "epic"]
         model: "opus"
       default_model: "sonnet"
   ```

5. **Copy and edit agent CLAUDE.md files** from `examples/kubestellar/agents/`. Template variables are substituted automatically at kick time — you don't need to hardcode your project values.

6. **Set agent `.env` files** with your workdir and model preferences.

7. **Start:**
   ```bash
   hive supervisor
   ```

### Template variables

Agent policy files (CLAUDE.md) support these template variables, substituted by `kick-agents.sh` at kick time:

| Variable | Source in config | Example value |
|----------|-----------------|---------------|
| `${PROJECT_ORG}` | `project.org` | `kubestellar` |
| `${PROJECT_PRIMARY_REPO}` | `project.primary_repo` | `kubestellar/console` |
| `${PROJECT_AI_AUTHOR}` | `project.ai_author` | `clubanderson` |
| `${PROJECT_REPOS_LIST}` | `project.repos` | `kubestellar/console kubestellar/docs ...` |
| `${HIVE_REPO}` | `project.hive_repo` | `kubestellar/hive` |
| `${GA4_PROPERTY_ID}` | `outreach.ga4.property_id` | `525401563` |
| `${AGENTS_WORKDIR}` | `agents.workdir` | `/home/dev/my-project` |
| `${BEADS_BASE}` | `agents.beads_base` | `/home/dev` |

### What agents receive on each kick

The kick script assembles context for each agent before dispatching:

- **Actionable issues** — filtered, classified, oldest-first
- **Actionable PRs** — grouped by cluster (related file overlap)
- **Merge-eligible PRs** — CI green, author validated, ready to merge
- **Health indicators** — only RED thresholds (failures that need attention)
- **GitHub rate limits** — remaining quota so agents can pace themselves
- **Policy instructions** — CLAUDE.md (cached if unchanged since last kick)
- **Current backend/model** — so agents know their own capabilities

---

## Config

`/etc/hive/hive.conf` — the only file you need to edit for basic setup:

```bash
# Repos to watch (space-separated)
HIVE_REPOS="owner/repo1 owner/repo2"

# Agent CLI backends to use (space-separated)
HIVE_BACKENDS="copilot"          # copilot claude gemini goose

# Local model services (optional — needs GPU or fast CPU)
# HIVE_MODEL_SERVICES="ollama litellm"

# Auto-install missing backends on hive supervisor start
HIVE_AUTO_INSTALL=true

# Notifications — set any combination
NTFY_TOPIC=your-secret-topic     # free at ntfy.sh
# SLACK_WEBHOOK=https://hooks.slack.com/services/...
# DISCORD_WEBHOOK=https://discord.com/api/webhooks/...
```

For advanced configuration (pipeline stages, classification rules, health checks, outreach, Discord), use `hive-project.yaml`. See `config/hive-project.yaml.example` for the full schema and `examples/kubestellar/` for a production reference.

---

## Services

### Docker (v2)

The Docker container runs a single Go binary (`hive`) that manages everything internally: agent tmux sessions, the governor eval loop, the dashboard server, health checks, and token tracking. No systemd units needed — `docker run` with `--restart unless-stopped` handles process lifecycle, and Watchtower handles image updates.

| Container | Image | Purpose |
|-----------|-------|---------|
| `hive` | `ghcr.io/kubestellar/hive:v2-latest` | Orchestrator + dashboard + agents |
| `watchtower` | `containrrr/watchtower` | Auto-pulls new images on push to v2 |

### Systemd (v1)

The legacy systemd install uses these units:

| Unit | Purpose |
|------|---------|
| `hive.service` | Main supervisor (legacy single-instance mode) |
| `hive@.service` | Per-agent template (`hive@scanner`, `hive@reviewer`, etc.) |
| `hive-deploy.service` / `.timer` | Auto-deploy from git every 60s |
| `hive-dashboard.service` | Web dashboard backend |
| `hive-discord.service` | Discord bot |
| `hive-snapshot.service` / `.timer` | Snapshot publication |
| `gh-zombie-reaper.service` / `.timer` | Cleans stale `gh` auth sessions |
| `ttyd-hive.service` | Web terminal (ttyd) for remote access |

macOS support uses launchd plists — see `launchd/` and `docs/macos.md`.

---

## Repository Layout

```
bin/                        # orchestration, pipeline, utilities
  hive.sh                   # main CLI
  supervisor.sh             # agent session manager with auto-restart
  kick-governor.sh          # adaptive cadence governor
  kick-agents.sh            # agent dispatcher with context assembly
  run-pipeline.sh           # deterministic pipeline runner
  enumerate-actionable.sh   # canonical work list enumerator
  merge-gate.sh             # merge eligibility checker
  issue-classifier.sh       # complexity/model assignment
  gh-wrapper.sh             # enforcer — wraps gh CLI
  gh-app-token.sh           # GitHub App token generator
  hive-deploy.sh            # auto-deploy from git
  notify.sh                 # ntfy/Slack/Discord notifications
  hive-config.sh            # shared config loader
  ...

config/                     # configuration templates
  hive-project.yaml.example # full schema with comments
  agent.env.example         # per-agent env template
  backends.conf             # backend definitions

dashboard/                  # web UI
  server.js                 # Node.js backend (40+ API endpoints, SSE)
  index.html                # single-page frontend
  health-check.sh           # config-driven health checks
  api-collector.sh          # issue-to-merge time metrics
  agent-metrics.sh          # per-agent busy time, restart counts
  agent-summaries.sh        # action summary extraction

discord/                    # Discord bot
  bot.js                    # event bridge + command router

examples/
  kubestellar/              # production reference implementation
    hive-project.yaml       # full config for 5 repos, 5 agents, GA4
    agents/                 # CLAUDE.md policies + .env files per agent

docs/
  architecture.md           # system architecture deep dive
  macos.md                  # macOS setup (launchd)
  troubleshooting.md        # common issues and fixes
  outreach-antispam.md      # anti-spam rules for outreach

systemd/                    # Linux service/timer units
launchd/                    # macOS LaunchAgent plists
```

---

## Troubleshooting

```bash
hive status                  # check what's running
hive logs governor           # why did it kick / not kick?
hive logs scanner            # what is scanner doing?
hive attach supervisor       # watch supervisor live
journalctl -u hive@scanner   # raw service log
```

### Common issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| All agents idle, no ntfy | Governor crashing | `hive logs governor` to diagnose |
| Governor: `Permission denied` on `/var/run/kick-governor/` | Root-owned files | `sudo chown -R dev:dev /var/run/kick-governor/` |
| Dashboard: agents show `stopped` / CLI `?` | Service running as root (can't see dev's tmux) | Add `User=dev` to `hive-dashboard.service` |
| Dashboard: widget download 404 | Stale node process on port 3001 | `ss -tlnp \| grep 3001` → kill → restart service |
| Mode change delayed by one cycle | Governor reading stale cache | Verify `actionable.json` exists in `/var/run/hive-metrics/` |
| Agent ignores model switch | CLI is pinned | `hive unpin <agent>` to release to governor control |
| Rate limited | Personal token exhausted | Configure GitHub App in `hive-project.yaml` for isolated rate limits |

See `docs/troubleshooting.md` for more.

---

Apache 2.0  ·  [Architecture](docs/architecture.md)  ·  [macOS Setup](docs/macos.md)  ·  [KubeStellar Example](examples/kubestellar/)
