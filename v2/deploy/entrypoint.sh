#!/bin/sh
set -e

export TZ="${TZ:-America/New_York}"
export HIVE_API_PORT="${HIVE_API_PORT:-3002}"
export HIVE_PROXY_PORT="${HIVE_PROXY_PORT:-3001}"
export HIVE_STATIC_DIR="${HIVE_STATIC_DIR:-/opt/hive/proxy/public}"

# ── Root-only setup (runs once, then re-execs as dev) ──────────────────
if [ "$(id -u)" = "0" ]; then
  # Fix ownership of mounted volumes (may be root-owned from host bind mounts)
  chown -R dev:dev /data /home/dev 2>/dev/null || true
  mkdir -p /var/run/hive-metrics && chown dev:dev /var/run/hive-metrics

  # Seed data files from image into /data if they don't already exist
  if [ -d /opt/hive/seed-data ]; then
    echo "[entrypoint] Seeding data files..."
    cp -rn /opt/hive/seed-data/* /data/ 2>/dev/null || true
    chown -R dev:dev /data 2>/dev/null || true
  fi

  # Create beads symlinks: /home/dev/<agent>-beads -> /data/beads/<agent>
  if [ -d /etc/hive/agents ] || [ -d /data/beads ]; then
    mkdir -p /home/dev /data/beads
    if [ -d /etc/hive/agents ]; then
      for envfile in /etc/hive/agents/*.env; do
        [ -f "$envfile" ] || continue
        agent="$(basename "$envfile" .env)"
        mkdir -p "/data/beads/${agent}"
        ln -sfn "/data/beads/${agent}" "/home/dev/${agent}-beads"
        echo "[entrypoint] Beads symlink: /home/dev/${agent}-beads -> /data/beads/${agent}"
      done
    fi
    for beaddir in /data/beads/*/; do
      [ -d "$beaddir" ] || continue
      agent="$(basename "$beaddir")"
      if [ ! -L "/home/dev/${agent}-beads" ]; then
        ln -sfn "/data/beads/${agent}" "/home/dev/${agent}-beads"
        echo "[entrypoint] Beads symlink: /home/dev/${agent}-beads -> /data/beads/${agent}"
      fi
    done
    chown -R dev:dev /data/beads /home/dev 2>/dev/null || true
  fi

  # Drop to non-root user for all runtime processes.
  # Claude Code refuses --dangerously-skip-permissions as root.
  if command -v gosu >/dev/null 2>&1; then
    echo "[entrypoint] Dropping to dev user"
    exec gosu dev "$0" "$@"
  else
    echo "[entrypoint] WARN: gosu not found, running as root"
  fi
fi

# ── Non-root setup and process launch (runs as dev) ────────────────────

# Ensure vault directories exist
mkdir -p /data/vaults
if [ -n "${HIVE_WIKI_GIT_URL:-}" ] && [ ! -d /data/vaults/hive-wiki/.git ]; then
  echo "[entrypoint] Cloning wiki vault from ${HIVE_WIKI_GIT_URL}..."
  git clone "${HIVE_WIKI_GIT_URL}" /data/vaults/hive-wiki 2>/dev/null || \
    echo "[entrypoint] Git clone failed — vault will be initialized empty"
fi
mkdir -p /data/vaults/hive-wiki

# Configure git identity and credential helper for GitHub App token
git config --global user.name "kubestellar-hive"
git config --global user.email "hive-bot@kubestellar.io"
git config --global --replace-all credential.helper ""
git config --global --replace-all "credential.https://github.com.helper" "/usr/local/bin/git-credential-hive.sh"

# Generate initial GitHub App token if credentials are available
if [ -x /usr/local/bin/hive-config.sh ]; then
  . /usr/local/bin/hive-config.sh 2>/dev/null || true
fi
if [ -n "${GH_APP_ID:-}" ] && [ -n "${GH_APP_INSTALLATION_ID:-}" ]; then
  echo "[entrypoint] Generating GitHub App token..."
  /usr/local/bin/gh-app-token.sh >/dev/null 2>&1 && \
    echo "[entrypoint] Token cached at /var/run/hive-metrics/gh-app-token.cache" || \
    echo "[entrypoint] WARN: GitHub App token generation failed"
  export HIVE_GITHUB_TOKEN="$(cat /var/run/hive-metrics/gh-app-token.cache 2>/dev/null || true)"
fi

echo "[entrypoint] Starting Go binary on :${HIVE_API_PORT} (uid=$(id -u))"
hive "$@" &
HIVE_PID=$!

sleep 1

echo "[entrypoint] Starting Node.js proxy on :${HIVE_PROXY_PORT} → :${HIVE_API_PORT}"
cd /opt/hive/proxy && node server.js &
PROXY_PID=$!

TTYD_PORT="${HIVE_TTYD_PORT:-7681}"
echo "[entrypoint] Starting ttyd on :${TTYD_PORT}"
ttyd -W -a -p "${TTYD_PORT}" -t fontSize=14 -t disableLeaveAlert=true /usr/local/bin/ttyd-tmux.sh &
TTYD_PID=$!

cleanup() {
  echo "[entrypoint] Shutting down..."
  kill "$TTYD_PID" 2>/dev/null || true
  kill "$PROXY_PID" 2>/dev/null || true
  kill "$HIVE_PID" 2>/dev/null || true
  wait "$HIVE_PID" 2>/dev/null || true
  wait "$PROXY_PID" 2>/dev/null || true
}
trap cleanup INT TERM

wait "$HIVE_PID"
