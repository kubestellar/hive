# Hive v2 — Container Deployment

Run hive as a container on any Docker/Podman host (Flatcar, Ubuntu, etc.).

## Quick Start

```bash
# Pull the image
docker pull ghcr.io/kubestellar/hive:latest

# Create config directory
mkdir -p /etc/hive
cp config/agent.env.example /etc/hive/agent.env
cp config/hive-project.yaml.example /etc/hive/hive-project.yaml
# Edit both files with your values

# Run
docker run -d \
  --name hive \
  --env-file /etc/hive/agent.env \
  -v /etc/hive:/etc/hive:ro \
  -v /data:/data \
  -p 3001:3001 \
  --tty \
  ghcr.io/kubestellar/hive:latest
```

## With Docker Compose

```bash
cd v2
cp ../config/agent.env.example .env
# Edit .env with your values
docker compose up -d
```

## Mount Points

| Path | Purpose | Mode |
|------|---------|------|
| `/etc/hive/` | Config: `agent.env`, `hive-project.yaml`, policies | Read-only |
| `/data/` | Variable data: logs, beads, repos, agent state | Read-write |
| `/data/logs/` | Heartbeat + supervisor logs | Read-write |
| `/data/repos/` | Cloned working repositories | Read-write |
| `/data/agents/` | Per-agent beads and scratch | Read-write |
| `/home/dev/.ssh/` | SSH keys for git+ssh (optional) | Read-only |

## Flatcar on Proxmox (via Knuckle)

1. Download the [Knuckle ISO](https://github.com/castrojo/knuckle/releases) (amd64 or arm64)
2. Create a Proxmox VM: upload ISO, boot, walk through the 9-step wizard
   - Select the **Docker** sysext when prompted
3. After install, SSH into the Flatcar host
4. Copy `v2/deploy/flatcar/hive.bu` and edit `/etc/hive/agent.env` with your secrets
5. The Butane config includes a systemd unit that auto-pulls and runs the container

```bash
# On the Flatcar host — the systemd unit handles everything:
sudo systemctl enable --now hive.service

# Check status
sudo systemctl status hive
docker logs -f hive

# Attach to the agent tmux session inside the container
docker exec -it hive tmux attach -t hive

# Dashboard
curl http://localhost:3001
```

## Environment Variables

See `config/agent.env.example` for the full list. Key variables:

| Variable | Required | Description |
|----------|----------|-------------|
| `AGENT_LAUNCH_CMD` | Yes | CLI command to start the AI agent |
| `AGENT_LOOP_PROMPT` | Yes | Startup prompt (executor or cron mode) |
| `ANTHROPIC_API_KEY` | Yes | Claude API key |
| `HIVE_GITHUB_TOKEN` | Yes | GitHub PAT or App token |
| `AGENT_SESSION_NAME` | No | tmux session name (default: `hive`) |
| `DISCORD_WEBHOOK` | No | Discord notification webhook |
| `NTFY_TOPIC` | No | ntfy.sh push notification topic |
| `DASHBOARD_PORT` | No | Dashboard port (default: `3001`) |

## Building Locally

```bash
docker build -t hive:local -f v2/Dockerfile .
```
