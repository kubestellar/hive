#!/bin/bash
# hive-setup.sh — Bootstrap a fresh Ubuntu 24.04 LXC into a Hive v2 instance.
#
# Usage:
#   curl -fsSL <raw-url>/hive-setup.sh | bash
#   # or
#   ./hive-setup.sh [--level 2] [--repo org/repo] [--agents "scanner reviewer"]
#
# Options:
#   --level N          ACMM maturity level (default: 2 = analysis only, no PRs)
#   --repo org/repo    Target repository (default: interactive prompt)
#   --agents "list"    Space-separated agent list (default: per-level defaults)
#   --git-name "name"  Git user.name for the dev user
#   --git-email "email" Git user.email for the dev user
#   --skip-auth        Skip interactive gh auth login (do it later)
#   --dry-run          Print what would be done without executing
#
# ACMM Levels and default agents:
#   L1: (local tooling only — no hive needed)
#   L2: supervisor scanner reviewer (analysis + issue filing, NO PRs)
#   L3: supervisor scanner reviewer architect (opens PRs)
#   L4: supervisor scanner reviewer architect outreach sec-check (full autonomy)
#   L5+: supervisor scanner reviewer architect outreach sec-check strategist analyst guardian

set -euo pipefail

ACMM_LEVEL=2
TARGET_REPO=""
AGENTS=""
GIT_NAME=""
GIT_EMAIL=""
SKIP_AUTH=false
DRY_RUN=false
HIVE_REPO_URL="https://github.com/kubestellar/hive.git"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --level)     ACMM_LEVEL="$2"; shift 2 ;;
    --repo)      TARGET_REPO="$2"; shift 2 ;;
    --agents)    AGENTS="$2"; shift 2 ;;
    --git-name)  GIT_NAME="$2"; shift 2 ;;
    --git-email) GIT_EMAIL="$2"; shift 2 ;;
    --skip-auth) SKIP_AUTH=true; shift ;;
    --dry-run)   DRY_RUN=true; shift ;;
    *)           echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Default agents by ACMM level
if [[ -z "$AGENTS" ]]; then
  case "$ACMM_LEVEL" in
    1) echo "L1 is local tooling — no hive server needed."; exit 0 ;;
    2) AGENTS="supervisor scanner reviewer" ;;
    3) AGENTS="supervisor scanner reviewer architect" ;;
    4) AGENTS="supervisor scanner reviewer architect outreach sec-check" ;;
    *) AGENTS="supervisor scanner reviewer architect outreach sec-check strategist analyst guardian" ;;
  esac
fi

log() { echo "[hive-setup] $*"; }
run() {
  if $DRY_RUN; then
    echo "[dry-run] $*"
  else
    eval "$@"
  fi
}

log "Hive v2 Setup — ACMM Level $ACMM_LEVEL"
log "Target repo: ${TARGET_REPO:-<will prompt>}"
log "Agents: $AGENTS"
echo ""

# ── Prompt for missing values ───────────────────────────────────────

if [[ -z "$TARGET_REPO" ]]; then
  read -rp "Target repository (org/repo): " TARGET_REPO
fi

if [[ -z "$GIT_NAME" ]]; then
  read -rp "Git user.name for commits: " GIT_NAME
fi

if [[ -z "$GIT_EMAIL" ]]; then
  read -rp "Git user.email for commits: " GIT_EMAIL
fi

TARGET_ORG="${TARGET_REPO%%/*}"
TARGET_REPO_NAME="${TARGET_REPO##*/}"

log "Configuration:"
log "  Repo: $TARGET_REPO"
log "  Org: $TARGET_ORG"
log "  Level: L$ACMM_LEVEL"
log "  Agents: $AGENTS"
log "  Git: $GIT_NAME <$GIT_EMAIL>"
echo ""

# ── Phase 1: System Packages ───────────────────────────────────────

log "Phase 1: Installing system packages..."

run "apt-get update -qq"

PACKAGES=(
  # Core
  git tmux jq curl wget bc sqlite3 htop ripgrep
  # Build tools
  build-essential make gcc g++
  # Python
  python3 python3-venv python3-pip
  # Misc
  unzip fzf tree
)

run "apt-get install -y -qq ${PACKAGES[*]}"

# ── Phase 2: Node.js 22 (via NodeSource) ───────────────────────────

log "Phase 2: Installing Node.js 22..."

if ! command -v node &>/dev/null || [[ "$(node --version | cut -d. -f1 | tr -d v)" -lt 22 ]]; then
  run "curl -fsSL https://deb.nodesource.com/setup_22.x | bash -"
  run "apt-get install -y -qq nodejs"
fi

log "  Node.js: $(node --version 2>/dev/null || echo 'pending')"
log "  npm: $(npm --version 2>/dev/null || echo 'pending')"

# ── Phase 3: GitHub CLI ────────────────────────────────────────────

log "Phase 3: Installing GitHub CLI..."

if ! command -v gh &>/dev/null; then
  run "curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg"
  run "echo 'deb [arch=\$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main' \
    | tee /etc/apt/sources.list.d/github-cli.list > /dev/null"
  run "apt-get update -qq && apt-get install -y -qq gh"
fi

log "  gh: $(gh --version 2>/dev/null | head -1 || echo 'pending')"

# ── Phase 4: Claude Code CLI ──────────────────────────────────────

log "Phase 4: Installing Claude Code CLI..."

if ! command -v claude &>/dev/null; then
  run "npm install -g @anthropic-ai/claude-code"
fi

log "  Claude: $(claude --version 2>/dev/null | head -1 || echo 'pending')"

# ── Phase 5: Create dev user ──────────────────────────────────────

log "Phase 5: Setting up 'dev' user..."

if ! id dev &>/dev/null; then
  run "useradd -m -s /bin/bash dev"
fi

run "sudo -u dev git config --global user.name '$GIT_NAME'"
run "sudo -u dev git config --global user.email '$GIT_EMAIL'"

# ── Phase 6: Clone Hive repo ─────────────────────────────────────

log "Phase 6: Cloning Hive repo..."

HIVE_DIR="/tmp/hive"
if [[ ! -d "$HIVE_DIR/.git" ]]; then
  run "git clone '$HIVE_REPO_URL' '$HIVE_DIR'"
else
  run "git -C '$HIVE_DIR' pull --rebase origin main"
fi

# ── Phase 7: Install Hive scripts ────────────────────────────────

log "Phase 7: Installing Hive scripts to /usr/local/bin/..."

HIVE_SCRIPTS=(
  supervisor.sh agent-launch.sh hive-config.sh kick-agents.sh
  agent-healthcheck.sh enumerate-actionable.sh merge-gate.sh
  gh-app-token.sh gh-rate-check.sh gh-zombie-reaper.sh
  notify.sh hive.sh hive-deploy.sh kick-governor.sh
  supervisor-kick.sh ttyd-tmux.sh conflict-sweeper.sh
  run-pipeline.sh issue-classifier.sh pr-cluster-detector.sh
  architecture-detector.sh copilot-comment-checker.sh
  outreach-tracker.sh ga4-anomaly-detector.sh
  kick-outcome-tracker.sh fetch-coverage.sh
  hive-prereq-check.sh
)

for script in "${HIVE_SCRIPTS[@]}"; do
  if [[ -f "$HIVE_DIR/bin/$script" ]]; then
    run "cp '$HIVE_DIR/bin/$script' '/usr/local/bin/$script'"
    run "chmod +x '/usr/local/bin/$script'"
  fi
done

# Install backends.conf
run "mkdir -p /usr/local/etc/hive"
if [[ -f "$HIVE_DIR/config/backends.conf" ]]; then
  run "cp '$HIVE_DIR/config/backends.conf' /usr/local/etc/hive/backends.conf"
fi

# Install gh wrapper (interposes on gh to enforce per-agent restrictions)
if [[ -f "$HIVE_DIR/bin/gh-wrapper.sh" ]]; then
  run "cp /usr/bin/gh /usr/bin/gh-real 2>/dev/null || true"
  run "cp '$HIVE_DIR/bin/gh-wrapper.sh' /usr/local/bin/gh"
  run "chmod +x /usr/local/bin/gh"
fi

# ── Phase 8: Create directories ──────────────────────────────────

log "Phase 8: Creating Hive directories..."

run "mkdir -p /etc/hive"
run "mkdir -p /var/run/hive && chown dev:dev /var/run/hive"
run "mkdir -p /var/run/hive-metrics && chown dev:dev /var/run/hive-metrics"
run "mkdir -p /home/dev/.local/state/hive && chown -R dev:dev /home/dev/.local"

# ── Phase 9: Clone target repo ───────────────────────────────────

log "Phase 9: Cloning target repo for dev user..."

TARGET_DIR="/home/dev/${TARGET_REPO_NAME}"
if [[ ! -d "$TARGET_DIR" ]]; then
  run "sudo -u dev git clone 'https://github.com/${TARGET_REPO}.git' '$TARGET_DIR'"
fi

# Create agent workdirs (beads directories)
for agent in $AGENTS; do
  run "sudo -u dev mkdir -p '/home/dev/${agent}-beads'"
done

# ── Phase 10: Generate hive-project.yaml ─────────────────────────

log "Phase 10: Generating hive-project.yaml..."

# Determine PR permissions based on ACMM level
PR_ENABLED=false
[[ "$ACMM_LEVEL" -ge 3 ]] && PR_ENABLED=true

cat > /etc/hive/hive-project.yaml << PROJEOF
# hive-project.yaml — Auto-generated by hive-setup.sh
# Target: ${TARGET_REPO} | ACMM Level: L${ACMM_LEVEL}

project:
  name: "${TARGET_REPO_NAME}"
  org: "${TARGET_ORG}"
  primary_repo: "${TARGET_REPO}"
  repos:
    - ${TARGET_REPO}
  ai_author: "${GIT_NAME}"
  website: ""
  hive_repo: "kubestellar/hive"

agents:
  enabled:
$(for a in $AGENTS; do echo "    - $a"; done)
  beads_base: "/home/dev"
  workdir: "${TARGET_DIR}"
  policy_dir: "agents"

permissions:
  open_issues: true
  open_prs: ${PR_ENABLED}
  merge_prs: false
  acmm_level: ${ACMM_LEVEL}

dashboard:
  title: "${TARGET_REPO_NAME} Hive"
  port: 3001
PROJEOF

# ── Phase 11: Generate hive-runtime.yaml ─────────────────────────

log "Phase 11: Generating hive-runtime.yaml..."

cat > /etc/hive/hive-runtime.yaml << RTEOF
agents:
  enabled:
$(for a in $AGENTS; do echo "    - $a"; done)
  sidebar:
    groups:
      - name: null
        agents:
          - supervisor
      - name: autonomy
        agents:
$(for a in $AGENTS; do [[ "$a" != "supervisor" ]] && echo "          - $a"; done)
project:
  repos:
    - ${TARGET_REPO}
RTEOF

# ── Phase 12: Generate agent .env files ──────────────────────────

log "Phase 12: Generating agent .env files..."

# L2 prompt suffix — analysis only, no PRs
L2_SUFFIX="You are operating at ACMM Level 2 (analysis only). You may open issues but MUST NOT open pull requests, push code, or modify any files in the repository. Your role is to study, analyze, and report."

for agent in $AGENTS; do
  ENVFILE="/etc/hive/${agent}.env"

  # Agent-specific defaults
  case "$agent" in
    supervisor)
      WORKDIR="/home/dev/hive-supervisor"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the Hive Supervisor for ${TARGET_REPO}. STEP 0: pull latest hive repo: git -C /tmp/hive pull --rebase origin main. STEP 1: re-read your policy file. STEP 2: hive status. STEP 3: check all agent panes. STEP 4: kick idle agents. STEP 5: report status. ${L2_SUFFIX}"
      STYLE="bg=colour196,fg=white"
      ;;
    scanner)
      WORKDIR="$TARGET_DIR"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the Scanner agent for ${TARGET_REPO}. Study the codebase, identify bugs, test gaps, security issues, and code quality problems. File issues for significant findings. ${L2_SUFFIX}"
      STYLE="bg=colour33,fg=white"
      ;;
    reviewer)
      WORKDIR="$TARGET_DIR"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the Reviewer agent for ${TARGET_REPO}. Review recent PRs and commits for quality, correctness, and potential regressions. File issues for problems found. ${L2_SUFFIX}"
      STYLE="bg=colour28,fg=white"
      ;;
    architect)
      WORKDIR="$TARGET_DIR"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the Architect agent for ${TARGET_REPO}. Analyze architecture, identify refactoring opportunities, and propose structural improvements via issues. ${L2_SUFFIX}"
      STYLE="bg=colour93,fg=white"
      ;;
    outreach)
      WORKDIR="$TARGET_DIR"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the Outreach agent for ${TARGET_REPO}. Study the ecosystem, identify adoption opportunities, and track community activity. ${L2_SUFFIX}"
      STYLE="bg=colour208,fg=white"
      ;;
    sec-check)
      WORKDIR="$TARGET_DIR"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the Security agent for ${TARGET_REPO}. Audit dependencies, scan for vulnerabilities, check for hardcoded secrets, and review security practices. File issues for findings. ${L2_SUFFIX}"
      STYLE="bg=colour160,fg=white"
      ;;
    *)
      WORKDIR="$TARGET_DIR"
      MODEL="claude-sonnet-4-6"
      PROMPT="You are the ${agent} agent for ${TARGET_REPO}. ${L2_SUFFIX}"
      STYLE="bg=colour240,fg=white"
      ;;
  esac

  cat > "$ENVFILE" << ENVEOF
# ${agent}.env — Hive agent config (auto-generated)
AGENT_USER=dev
AGENT_SESSION_NAME=${agent}
AGENT_WORKDIR=${WORKDIR}
AGENT_LAUNCH_CMD="agent-launch.sh --backend claude --model ${MODEL}"
AGENT_CLI=claude

AGENT_POLL_SEC=10
AGENT_LOOP_PROMPT="${PROMPT}"

AGENT_LOG_FILE=/home/dev/.local/state/hive/${agent}-heartbeat.log
AGENT_STALE_MAX_SEC=3600
AGENT_MAX_RESPAWNS=3

AGENT_TMUX_STATUS_STYLE="${STYLE}"
AGENT_CLAUDE_RENAME_TO=${agent^}

AGENT_RATE_LIMIT_FAILOVER=false
ENVEOF
done

# Create supervisor workdir
run "sudo -u dev mkdir -p /home/dev/hive-supervisor"
run "sudo -u dev git -C /home/dev/hive-supervisor init 2>/dev/null || true"

# ── Phase 13: Install systemd units ──────────────────────────────

log "Phase 13: Installing systemd units..."

for unit in hive.service hive@.service; do
  if [[ -f "$HIVE_DIR/systemd/$unit" ]]; then
    run "cp '$HIVE_DIR/systemd/$unit' '/etc/systemd/system/$unit'"
  fi
done

run "systemctl daemon-reload"

# Enable agent services
for agent in $AGENTS; do
  run "systemctl enable 'hive@${agent}.service' 2>/dev/null || true"
done

# ── Phase 14: GitHub Auth ────────────────────────────────────────

log "Phase 14: GitHub authentication..."

if ! $SKIP_AUTH; then
  echo ""
  echo "  GitHub authentication is needed for the 'dev' user."
  echo "  The gh CLI will prompt you to log in."
  echo ""
  run "sudo -u dev gh auth login" || warn "gh auth skipped — run manually: sudo -u dev gh auth login"
fi

# ── Phase 15: Run prereq check ───────────────────────────────────

log "Phase 15: Running prereq check..."
echo ""
run "bash /usr/local/bin/hive-prereq-check.sh" || true

# ── Done ─────────────────────────────────────────────────────────

echo ""
log "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log "Hive v2 setup complete!"
log ""
log "  Target repo:  ${TARGET_REPO}"
log "  ACMM Level:   L${ACMM_LEVEL}"
log "  Agents:       ${AGENTS}"
log "  Config:       /etc/hive/"
log "  Repo clone:   ${TARGET_DIR}"
log ""
log "Next steps:"
log "  1. Authenticate Claude Code:  sudo -u dev claude auth login"
log "  2. Copy agent CLAUDE.md files to /etc/hive/ (or use defaults)"
log "  3. Start agents:  systemctl start hive@supervisor"
log "  4. Attach to session:  tmux attach -t supervisor"
log ""
if [[ "$ACMM_LEVEL" -le 2 ]]; then
  log "  NOTE: L${ACMM_LEVEL} mode — agents will analyze and file issues"
  log "  but will NOT open PRs or modify code."
fi
log "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
