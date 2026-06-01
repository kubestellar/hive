#!/bin/sh
set -e

export TZ="${TZ:-America/New_York}"
export HIVE_API_PORT="${HIVE_API_PORT:-3002}"
export HIVE_PROXY_PORT="${HIVE_PROXY_PORT:-3001}"
export HIVE_STATIC_DIR="${HIVE_STATIC_DIR:-/opt/hive/proxy/public}"

# ── Root-only setup (runs once, then re-execs as dev) ──────────────────
if [ "$(id -u)" = "0" ]; then
  # Fix ownership of mounted volumes (may be root-owned from host bind mounts)
  chown -R dev:node /data /home/dev 2>/dev/null || true
  chown dev:node /etc/hive/hive.yaml 2>/dev/null || true
  mkdir -p /var/run/hive-metrics && chown dev:node /var/run/hive-metrics 2>/dev/null || true

  # Copy read-only mounted secrets so dev user can read them
  if [ -f /etc/hive/gh-app-key.pem ]; then
    cp /etc/hive/gh-app-key.pem /var/run/hive-metrics/gh-app-key.pem
    chown dev:node /var/run/hive-metrics/gh-app-key.pem
    chmod 400 /var/run/hive-metrics/gh-app-key.pem
    export GH_APP_KEY_FILE=/var/run/hive-metrics/gh-app-key.pem
  fi

  # Seed data files from image into /data if they don't already exist
  if [ -d /opt/hive/seed-data ]; then
    echo "[entrypoint] Seeding data files..."
    cp -rn /opt/hive/seed-data/* /data/ 2>/dev/null || true
    chown -R dev:node /data 2>/dev/null || true
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
    chown -R dev:node /data/beads /home/dev 2>/dev/null || true
    chmod -R g+rwX /data/beads 2>/dev/null || true
  fi

  # Shared CLI auth/cache lives in /data/home (persistent volume).
  # Make it group-writable so all agent UIDs (node group) can use it.
  # The manager sets HOME=/data/home for agent tmux sessions.
  mkdir -p /data/home/.config /data/home/.copilot /data/config/github-copilot /home/dev/.config
  ln -sfn /data/config/github-copilot /home/dev/.config/github-copilot
  ln -sfn /data/config/github-copilot /data/home/.config/github-copilot
  ln -sfn /data/home/.copilot /home/dev/.copilot
  chmod -R g+rwX /data/home 2>/dev/null || true
  # setgid on .copilot so new files inherit group node (agents share this dir)
  chmod g+s /data/home/.copilot 2>/dev/null || true
  chown -R dev:node /data/config /data/home /home/dev/.config 2>/dev/null || true
  # Default ACL: force group rw on every file copilot creates in .copilot/.
  # Copilot CLI rewrites config.json with 0600 on every token refresh,
  # locking out other agent UIDs. The default ACL overrides the app's mode
  # at the kernel level — no race condition, no polling needed.
  if command -v setfacl >/dev/null 2>&1; then
    setfacl -d -m g::rw /data/home/.copilot 2>/dev/null && \
      echo "[entrypoint] ACL: default g::rw on /data/home/.copilot" || \
      echo "[entrypoint] WARN: setfacl failed on /data/home/.copilot"
  else
    echo "[entrypoint] WARN: setfacl not found, config.json perm fix unavailable"
  fi
  echo "[entrypoint] CLI config: /data/home (shared, group-writable for agent UIDs)"

  # ── Per-agent UID isolation ──────────────────────────────────────────
  # Extract agent names from config + pack YAML, create system users,
  # write UID map, and set up iptables to force all outbound :443
  # through the MITM proxy (so agents can't bypass it via unset HTTPS_PROXY).
  HIVE_CONFIG="${HIVE_CONFIG:-/etc/hive/hive.yaml}"
  HIVE_UID_BASE=2001
  PROXY_UID=1001

  # Collect agent names from hive.yaml (map keys) and pack YAML (list items)
  AGENT_NAMES=""
  if [ -f "$HIVE_CONFIG" ]; then
    AGENT_NAMES=$(python3 -c "
import yaml, sys, os
names = set()
with open('$HIVE_CONFIG') as f:
    cfg = yaml.safe_load(f) or {}
agents = cfg.get('agents', {})
if isinstance(agents, dict):
    names.update(agents.keys())
elif isinstance(agents, list):
    for a in agents:
        if isinstance(a, dict) and 'name' in a:
            names.add(a['name'])
# Also check pack YAML if HIVE_LEVEL is set
level = os.environ.get('HIVE_LEVEL', '')
if level:
    import glob
    for p in glob.glob('/opt/hive/packs/level-*.yaml') + glob.glob('/data/packs/level-*.yaml'):
        try:
            with open(p) as pf:
                pack = yaml.safe_load(pf) or {}
            pack_agents = pack.get('agents', [])
            if isinstance(pack_agents, list):
                for a in pack_agents:
                    if isinstance(a, dict) and 'name' in a:
                        names.add(a['name'])
            elif isinstance(pack_agents, dict):
                names.update(pack_agents.keys())
        except Exception:
            pass
print('\n'.join(sorted(names)))
" 2>/dev/null) || true
  fi

  if [ -n "$AGENT_NAMES" ]; then
    echo "[entrypoint] Creating per-agent users for UID isolation..."
    mkdir -p /var/run/hive
    UID_OFFSET=0
    UID_JSON='{"agents":{'
    FIRST=true
    echo "$AGENT_NAMES" | while read -r agent_name; do
      [ -z "$agent_name" ] && continue
      AGENT_UID=$((HIVE_UID_BASE + UID_OFFSET))
      if ! id "hive-${agent_name}" >/dev/null 2>&1; then
        useradd --system -u "$AGENT_UID" -g node -d /data/home -M -s /bin/bash "hive-${agent_name}" 2>/dev/null || true
      fi
      mkdir -p "/data/agents/${agent_name}"
      chown -R "hive-${agent_name}:node" "/data/agents/${agent_name}" 2>/dev/null || true
      # Also fix beads dir ownership so agent can write beads.json
      if [ -d "/data/beads/${agent_name}" ]; then
        chown -R "hive-${agent_name}:node" "/data/beads/${agent_name}" 2>/dev/null || true
      fi
      echo "[entrypoint] Agent user: hive-${agent_name} (UID ${AGENT_UID})"
      UID_OFFSET=$((UID_OFFSET + 1))
    done

    # Write uid-map.json using python for proper JSON
    python3 -c "
import json, os
names = '''$AGENT_NAMES'''.strip().split('\n')
names = [n for n in names if n]
agents = {}
for i, name in enumerate(sorted(names)):
    agents[name] = $HIVE_UID_BASE + i
uid_map = {
    'agents': agents,
    'proxy_uid': $PROXY_UID,
    'base_uid': $HIVE_UID_BASE,
    'iptables_active': False
}
os.makedirs('/var/run/hive', exist_ok=True)
with open('/var/run/hive/uid-map.json', 'w') as f:
    json.dump(uid_map, f, indent=2)
print('[entrypoint] UID map written to /var/run/hive/uid-map.json')
" 2>/dev/null || echo "[entrypoint] WARN: Failed to write UID map"

    # Set up iptables: redirect all outbound :443 to the MITM proxy port,
    # except traffic from the proxy itself (UID 1001 / dev user).
    PROXY_PORT=18443
    if command -v iptables >/dev/null 2>&1; then
      if iptables -t nat -N HIVE_PROXY 2>/dev/null; then
        iptables -t nat -A HIVE_PROXY -m owner --uid-owner 0 -j RETURN
        iptables -t nat -A HIVE_PROXY -m owner --uid-owner "$PROXY_UID" -j RETURN
        iptables -t nat -A HIVE_PROXY -p tcp --dport 443 -j REDIRECT --to-ports "$PROXY_PORT"
        iptables -t nat -A OUTPUT -j HIVE_PROXY
        echo "[entrypoint] iptables: outbound :443 -> :${PROXY_PORT} (proxy UID ${PROXY_UID} exempt)"
        # Update uid-map to record iptables active
        python3 -c "
import json
with open('/var/run/hive/uid-map.json') as f:
    m = json.load(f)
m['iptables_active'] = True
with open('/var/run/hive/uid-map.json', 'w') as f:
    json.dump(m, f, indent=2)
" 2>/dev/null || true
      else
        echo "[entrypoint] WARN: iptables chain creation failed (need NET_ADMIN capability)"
      fi
    else
      echo "[entrypoint] WARN: iptables not found, proxy enforcement is advisory-only"
    fi
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
# Use the dev-readable copy if the configured key file isn't readable
if [ -n "${GH_APP_KEY_FILE:-}" ] && [ ! -r "$GH_APP_KEY_FILE" ]; then
  if [ -r /var/run/hive-metrics/gh-app-key.pem ]; then
    export GH_APP_KEY_FILE=/var/run/hive-metrics/gh-app-key.pem
  fi
fi
if [ -n "${GH_APP_ID:-}" ] && [ -n "${GH_APP_INSTALLATION_ID:-}" ]; then
  echo "[entrypoint] Generating GitHub App token..."
  /usr/local/bin/gh-app-token.sh >/dev/null 2>&1 && \
    echo "[entrypoint] Token cached at /var/run/hive-metrics/gh-app-token.cache" || \
    echo "[entrypoint] WARN: GitHub App token generation failed"
  export HIVE_GITHUB_TOKEN="$(cat /var/run/hive-metrics/gh-app-token.cache 2>/dev/null || true)"
fi

# Load Copilot PAT from persistent volume so the Go binary can inject it
# into agent tmux sessions via COPILOT_GITHUB_TOKEN env var.
COPILOT_PAT_FILE="/data/copilot-token-pat"
if [ -f "$COPILOT_PAT_FILE" ] && [ -s "$COPILOT_PAT_FILE" ]; then
  export COPILOT_GITHUB_TOKEN
  COPILOT_GITHUB_TOKEN="$(cat "$COPILOT_PAT_FILE")"
  echo "[entrypoint] Copilot PAT loaded from $COPILOT_PAT_FILE"
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
# Wrap ttyd in a respawn loop: ttyd exits on SIGHUP (its close signal),
# and orphaned LISTEN sockets block rebind, so we wait before retrying.
TTYD_RESPAWN_DELAY_SECS=5
(
  trap '' HUP
  while true; do
    ttyd -W -a -p "${TTYD_PORT}" -t fontSize=14 -t disableLeaveAlert=true /usr/local/bin/ttyd-tmux.sh
    echo "[entrypoint] ttyd exited (rc=$?), respawning in ${TTYD_RESPAWN_DELAY_SECS}s..."
    sleep "$TTYD_RESPAWN_DELAY_SECS"
  done
) &
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
