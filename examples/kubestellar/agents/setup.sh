#!/bin/bash
# setup-kubestellar-agents.sh — bootstrap the multi-agent KubeStellar deployment
#
# Run on the home server (Proxmox container). Creates directories, clones repos,
# initializes beads ledger, installs scanner cron, and starts all 5 agent instances.
#
# Usage:
#   ./setup.sh [--ntfy-topic <topic>]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENTS_HOME="${HOME}/.kubestellar-agents"
SCANNER_HOME="${HOME}/.kubestellar-fix-loop"
LEDGER_HOME="${HOME}/agent-ledger"
NTFY_TOPIC="${1:-}"

AGENTS=(supervisor fixer architect reviewer outreacher)
WORKER_AGENTS=(fixer architect reviewer outreacher)

log() { printf '\033[1;36m==> %s\033[0m\n' "$*"; }
err() { printf '\033[1;31mERR: %s\033[0m\n' "$*" >&2; exit 1; }

# Validate
command -v tmux >/dev/null || err "tmux not found: apt install tmux"
command -v hive >/dev/null || err "hive not found"
command -v gh >/dev/null || err "gh CLI not found: https://cli.github.com"
command -v bd >/dev/null || err "beads CLI not found: https://github.com/steveyegge/beads"
command -v sqlite3 >/dev/null || err "sqlite3 not found"

# 1. Create directories
log "Creating agent directories"
for agent in "${AGENTS[@]}"; do
  mkdir -p "$AGENTS_HOME/$agent"
done

# 2. Clone repos for mutating agents
log "Cloning repos for worker agents"
for agent in fixer architect; do
  for repo in console console-kb console-marketplace docs; do
    target="$AGENTS_HOME/$agent/$repo"
    if [ -d "$target/.git" ]; then
      echo "  ✓ $agent/$repo exists"
    else
      echo "  → Cloning $repo for $agent..."
      git clone "https://github.com/kubestellar/$repo.git" "$target" 2>/dev/null || echo "  ⚠ Clone failed for $repo"
    fi
  done
done

# Read-only clones for reviewer/outreacher (only console needed)
for agent in reviewer outreacher; do
  target="$AGENTS_HOME/$agent/console"
  if [ -d "$target/.git" ]; then
    echo "  ✓ $agent/console exists"
  else
    echo "  → Cloning console for $agent..."
    git clone "https://github.com/kubestellar/console.git" "$target" 2>/dev/null || echo "  ⚠ Clone failed"
  fi
done

# 3. Initialize supervisor as git repo with CLAUDE.md
log "Setting up supervisor"
cd "$AGENTS_HOME/supervisor"
cp "$SCRIPT_DIR/supervisor-CLAUDE.md" CLAUDE.md
if [ ! -d .git ]; then
  git init -q && git add CLAUDE.md && git commit -q -m "init: supervisor CLAUDE.md"
fi

# 4. Copy executor CLAUDE.md files
log "Installing CLAUDE.md for each executor"
for agent in "${WORKER_AGENTS[@]}"; do
  if [ -d "$AGENTS_HOME/$agent/console" ]; then
    cp "$SCRIPT_DIR/${agent}-CLAUDE.md" "$AGENTS_HOME/$agent/console/CLAUDE.md"
    echo "  ✓ $agent"
  fi
done

# 5. Initialize beads ledger
log "Initializing beads ledger"
mkdir -p "$LEDGER_HOME"
if [ ! -d "$LEDGER_HOME/.bd" ]; then
  cd "$LEDGER_HOME" && bd init
  echo "  ✓ Ledger initialized"
else
  echo "  ✓ Ledger already exists"
fi

# 6. Set up scanner
log "Setting up scanner"
mkdir -p "$SCANNER_HOME"
if [ ! -f "$SCANNER_HOME/worker.sh" ]; then
  cp "$SCRIPT_DIR/../worker.sh" "$SCANNER_HOME/worker.sh"
  chmod +x "$SCANNER_HOME/worker.sh"
fi
# Symlink state.db to supervisor
ln -sf "$SCANNER_HOME/state.db" "$AGENTS_HOME/supervisor/state.db" 2>/dev/null || true

# Install cron (Linux)
if command -v crontab >/dev/null && [ "$(uname)" = "Linux" ]; then
  if ! crontab -l 2>/dev/null | grep -q "kubestellar-fix-loop"; then
    (crontab -l 2>/dev/null; echo "*/15 * * * * $SCANNER_HOME/worker.sh >> $SCANNER_HOME/worker.log 2>&1") | crontab -
    echo "  ✓ Scanner cron installed (every 15 min)"
  else
    echo "  ✓ Scanner cron already installed"
  fi
fi

# 7. Install hive instances
log "Installing hive instances"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
for agent in "${AGENTS[@]}"; do
  env_src="$SCRIPT_DIR/${agent}.env"
  env_dst="/etc/hive/ks-${agent}.env"

  # Set NTFY_TOPIC if provided
  if [ -n "$NTFY_TOPIC" ]; then
    sed "s/^NTFY_TOPIC=$/NTFY_TOPIC=$NTFY_TOPIC/" "$env_src" | sudo tee "$env_dst" > /dev/null
  else
    sudo cp "$env_src" "$env_dst"
  fi

  sudo "$REPO_ROOT/install.sh" --instance "ks-${agent}" && echo "  ✓ ks-${agent} installed" || echo "  ⚠ ks-${agent} failed"
done

# 8. Summary
log "Setup complete!"
echo ""
echo "┌──────────────────────────────────────────────┐"
echo "│  KubeStellar Multi-Agent System              │"
echo "├──────────────────────────────────────────────┤"
echo "│  🎯 ks-supervisor  (Opus, /loop 1m)          │"
echo "│  🔧 ks-fixer       (Sonnet, executor)        │"
echo "│  🏗️  ks-architect   (Sonnet, executor)        │"
echo "│  👁️  ks-reviewer    (Sonnet, executor)        │"
echo "│  📣 ks-outreacher  (Sonnet, executor)        │"
echo "├──────────────────────────────────────────────┤"
echo "│  Scanner: cron every 15 min                  │"
echo "│  Beads: ~/agent-ledger/                      │"
echo "│  State: ~/.kubestellar-fix-loop/state.db     │"
echo "└──────────────────────────────────────────────┘"
echo ""
echo "Attach: tmux attach -t ks-supervisor"
echo "Status: for s in ks-{supervisor,fixer,architect,reviewer,outreacher}; do echo \"\$s: \$(tmux has-session -t \$s 2>/dev/null && echo ✅ || echo ❌)\"; done"
