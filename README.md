# Hive

**AI agent orchestration for open source. Point it at a GitHub repo, configure a team of specialized agents, and let the governor decide what needs doing.**

> **Hive v2 is the active branch.** All development, documentation, and releases are on the [`v2` branch](https://github.com/kubestellar/hive/tree/v2).

## Quick Start

```bash
git clone -b v2 https://github.com/kubestellar/hive.git
cd hive/v2

cp hive.yaml.example hive.yaml
# Edit hive.yaml: set your org, repos, and GitHub token

export HIVE_GITHUB_TOKEN=ghp_your_token_here
docker compose up -d

# Dashboard at http://localhost:3001
```

The default `docker-compose.yaml` pulls the pre-built image `ghcr.io/kubestellar/hive:v2-latest`. To build from source instead, run `docker compose build` before `docker compose up -d`.

## Links

- **Hub**: [hive.kubestellar.io](https://hive.kubestellar.io) — registry, dashboard, hosted hives
- **Get Started**: [hive.kubestellar.io/get-started](https://hive.kubestellar.io/get-started)
- **v2 README**: [Full documentation](https://github.com/kubestellar/hive/tree/v2/v2#readme)
- **Paper**: [The AI Codebase Maturity Model (arXiv)](https://arxiv.org/abs/2604.09388)

## What It Does

- Runs specialized AI agents (scanner, quality, architect, ci-maintainer, security, outreach) on your GitHub repos
- A governor evaluates actionable issues/PRs and kicks agents on adaptive cadences
- ACMM (Agent Capability Maturity Model) graduates from advisory-only to fully autonomous
- An ACMM proxy enforces mode constraints — advisory agents cannot create PRs
- Contributors connect their CLI and earn trust through completed tasks
- Hub coordinates a network of hives with federated contributor pools and a global leaderboard

## Contributing

See the [v2 branch](https://github.com/kubestellar/hive/tree/v2) for all contribution instructions.
