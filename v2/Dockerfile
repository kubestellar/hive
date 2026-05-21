FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY .git /repo/.git
COPY v2/ .
RUN GIT_HASH=$(git -C /repo rev-parse HEAD 2>/dev/null || echo "unknown") && \
    GIT_SHORT=$(git -C /repo rev-parse --short HEAD 2>/dev/null || echo "unknown") && \
    CGO_ENABLED=0 go build -ldflags "-X main.gitHash=${GIT_HASH} -X main.gitShort=${GIT_SHORT}" -o /hive ./cmd/hive
RUN CGO_ENABLED=0 go build -o /bd ./cmd/bd

FROM node:24-slim

# --- Layer 1: system packages (rarely changes) ---
RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates bash curl tmux bzip2 gosu \
 && rm -rf /var/lib/apt/lists/*

# Non-root user for agent processes (Claude Code refuses --dangerously-skip-permissions as root)
RUN groupadd -g 1000 dev && useradd -m -u 1000 -g dev -s /bin/bash dev

# --- Layer 2: ttyd (stable, infrequent releases) ---
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then TTYD_ARCH=x86_64; \
    elif [ "$ARCH" = "aarch64" ]; then TTYD_ARCH=aarch64; \
    else TTYD_ARCH=x86_64; fi && \
    curl -sL -o /usr/local/bin/ttyd \
      "https://github.com/tsl0922/ttyd/releases/latest/download/ttyd.${TTYD_ARCH}" && \
    chmod +x /usr/local/bin/ttyd

# --- Layer 3: GitHub Copilot CLI (frequent updates) ---
ARG COPILOT_VERSION=latest
RUN npm install -g @github/copilot@${COPILOT_VERSION} && npm cache clean --force || \
    echo "WARN: GitHub Copilot CLI install failed — skipping"

# --- Layer 7: Claude Code (most frequent updates) ---
ARG CLAUDE_CODE_VERSION=latest
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} && npm cache clean --force

# --- Application layers ---
ENV HOME=/home/dev

COPY --from=builder /hive /usr/local/bin/hive
COPY --from=builder /bd /usr/local/bin/bd

COPY v2/proxy/ /opt/hive/proxy/
RUN cd /opt/hive/proxy && npm install --omit=dev

COPY v2/pkg/dashboard/static/index.html /opt/hive/proxy/public/

COPY examples/kubestellar/hive-project.yaml /etc/hive/hive-project.yaml
COPY examples/acmm/ /opt/hive/examples/acmm/
COPY examples/agents/ /opt/hive/examples/agents/
COPY v2/deploy/data/ /opt/hive/seed-data/
COPY v2/deploy/ttyd-tmux.sh /usr/local/bin/ttyd-tmux.sh
COPY bin/gh-app-token.sh bin/hive-config.sh bin/agent-launch.sh bin/git-credential-hive.sh /usr/local/bin/
COPY bin/gh-wrapper.sh /usr/local/bin/gh
RUN chmod +x /usr/local/bin/ttyd-tmux.sh /usr/local/bin/gh-app-token.sh \
    /usr/local/bin/hive-config.sh /usr/local/bin/agent-launch.sh /usr/local/bin/gh \
    /usr/local/bin/git-credential-hive.sh

# --- Nous framework (strategist experiment engine) ---
RUN (apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-venv python3-pip jq && rm -rf /var/lib/apt/lists/*) || \
    echo "WARN: apt-get failed — Nous framework will not be available"
COPY bin/nous-runner.sh bin/nous-sync.py bin/nous-hive-gate.py bin/nous-install.sh /tmp/hive/bin/
RUN chmod +x /tmp/hive/bin/*.sh
COPY examples/kubestellar/nous-campaign.yaml examples/kubestellar/nous-governor-campaign.yaml examples/kubestellar/nous-repo-campaign.yaml /etc/hive/
RUN mkdir -p /var/run/nous/governor /var/run/nous/repo /data/nous/snapshots /opt/nous
RUN pip3 install --no-cache-dir --break-system-packages pyyaml 2>/dev/null || \
    pip3 install --no-cache-dir pyyaml 2>/dev/null || \
    echo "WARN: system pyyaml install failed"
RUN git clone -b feat/cli-agent-mode https://github.com/clubanderson/agentic-strategy-evolution /opt/nous && \
    python3 -m venv /opt/nous/venv && \
    /opt/nous/venv/bin/pip install --no-cache-dir -e /opt/nous 2>&1 || \
    echo "WARN: Nous pip install failed — run_campaign.py may not work"

EXPOSE 3001 3002 7681
VOLUME ["/data", "/secrets"]

COPY v2/deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

RUN chown -R dev:dev /data /home/dev 2>/dev/null || true

ENTRYPOINT ["entrypoint.sh"]
CMD ["--config", "/etc/hive/hive.yaml"]
