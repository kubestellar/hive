FROM debian:bookworm-slim AS base

ARG TARGETARCH

# Runtime dependencies: tmux for session management, gh for GitHub API,
# jq/curl for scripting, node for dashboard, git for repo ops, python3
# for token-usage and nous scripts, openssh-client for git+ssh.
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash \
      ca-certificates \
      curl \
      git \
      jq \
      openssh-client \
      procps \
      python3 \
      python3-pip \
      sudo \
      tmux \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 22 LTS (dashboard + GA4 scripts)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=${TARGETARCH} signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI (default AI backend)
RUN npm install -g @anthropic-ai/claude-code

# Non-root user for agent processes
RUN groupadd -g 1000 dev && useradd -m -u 1000 -g dev -s /bin/bash dev

# Directory layout:
#   /opt/hive/bin/       — hive scripts (from repo)
#   /opt/hive/config/    — default config templates
#   /opt/hive/dashboard/ — dashboard static + server
#   /opt/hive/discord/   — discord bot
#   /etc/hive/           — runtime config (mounted from host)
#   /data/               — persistent variable data (mounted volume)
#   /data/logs/          — heartbeat + supervisor logs
#   /data/agents/        — per-agent beads, state, scratch
#   /data/repos/         — cloned working repos
RUN mkdir -p /opt/hive /etc/hive /data/logs /data/agents /data/repos /var/run/hive \
    && chown -R dev:dev /data /var/run/hive \
    && echo "dev ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/dev

COPY bin/        /opt/hive/bin/
COPY config/     /opt/hive/config/
COPY dashboard/  /opt/hive/dashboard/
COPY discord/    /opt/hive/discord/

# Symlink scripts into PATH
RUN for f in /opt/hive/bin/*.sh; do \
      ln -sf "$f" "/usr/local/bin/$(basename "$f")"; \
    done

# Dashboard dependencies
RUN cd /opt/hive/dashboard && [ -f package.json ] && npm ci --omit=dev || true

# hive CLI entrypoint
RUN ln -sf /opt/hive/bin/hive.sh /usr/local/bin/hive

WORKDIR /data
USER dev

# Dashboard port
EXPOSE 3001

# Health check: supervisor writes heartbeat; stale = unhealthy
HEALTHCHECK --interval=60s --timeout=5s --start-period=120s --retries=3 \
  CMD find /data/logs -name "heartbeat.log" -mmin -30 | grep -q . || exit 1

ENTRYPOINT ["/opt/hive/bin/container-entrypoint.sh"]
