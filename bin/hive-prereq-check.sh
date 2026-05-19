#!/bin/bash
# hive-prereq-check.sh — Validate that a host has all prerequisites for Hive v2.
#
# Usage: ./hive-prereq-check.sh [--fix]
#   --fix   Attempt to install missing prerequisites automatically
#
# Exit codes:
#   0  All prerequisites met
#   1  Missing prerequisites (printed to stdout)

set -euo pipefail

FIX_MODE=false
[[ "${1:-}" == "--fix" ]] && FIX_MODE=true

PASS=0
FAIL=0
WARN=0
FIXES_APPLIED=0

pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); }
warn() { echo "  △ $1"; WARN=$((WARN + 1)); }

try_fix() {
  if $FIX_MODE; then
    echo "    → fixing: $1"
    eval "$1" 2>&1 | sed 's/^/    /' || { fail "fix failed: $1"; return 1; }
    FIXES_APPLIED=$((FIXES_APPLIED + 1))
    return 0
  fi
  return 1
}

# ── OS ──────────────────────────────────────────────────────────────
echo "OS & Environment"

if [[ -f /etc/os-release ]]; then
  . /etc/os-release
  if [[ "$ID" == "ubuntu" && "${VERSION_ID%%.*}" -ge 24 ]]; then
    pass "Ubuntu ${VERSION_ID} (${PRETTY_NAME})"
  else
    warn "OS is ${PRETTY_NAME} — tested on Ubuntu 24.04 LTS"
  fi
else
  warn "Cannot detect OS (/etc/os-release missing)"
fi

CORES=$(nproc 2>/dev/null || echo 0)
if [[ "$CORES" -ge 4 ]]; then
  pass "CPU cores: ${CORES} (minimum 4)"
else
  fail "CPU cores: ${CORES} (need at least 4)"
fi

MEM_MB=$(free -m 2>/dev/null | awk '/^Mem:/ {print $2}' || echo 0)
if [[ "$MEM_MB" -ge 8000 ]]; then
  pass "RAM: ${MEM_MB}MB (minimum 8GB)"
elif [[ "$MEM_MB" -ge 4000 ]]; then
  warn "RAM: ${MEM_MB}MB (8GB+ recommended, 4GB minimum)"
else
  fail "RAM: ${MEM_MB}MB (need at least 4GB)"
fi

DISK_AVAIL_KB=$(df / 2>/dev/null | awk 'NR==2 {print $4}' || echo 0)
DISK_AVAIL_GB=$((DISK_AVAIL_KB / 1048576))
if [[ "$DISK_AVAIL_GB" -ge 20 ]]; then
  pass "Disk available: ${DISK_AVAIL_GB}GB (minimum 20GB)"
elif [[ "$DISK_AVAIL_GB" -ge 10 ]]; then
  warn "Disk available: ${DISK_AVAIL_GB}GB (20GB+ recommended)"
else
  fail "Disk available: ${DISK_AVAIL_GB}GB (need at least 10GB)"
fi

# ── Users ───────────────────────────────────────────────────────────
echo ""
echo "Users"

if id dev &>/dev/null; then
  pass "User 'dev' exists"
else
  fail "User 'dev' does not exist"
  try_fix "useradd -m -s /bin/bash dev" && pass "User 'dev' created"
fi

# ── Core Tools ──────────────────────────────────────────────────────
echo ""
echo "Core Tools"

check_cmd() {
  local name="$1" cmd="${2:-$1}" min_ver="${3:-}" install_cmd="${4:-}"
  if command -v "$cmd" &>/dev/null; then
    local ver
    ver=$("$cmd" --version 2>/dev/null | head -1 || echo "installed")
    pass "$name: $ver"
  else
    fail "$name: not found"
    [[ -n "$install_cmd" ]] && try_fix "$install_cmd" && pass "$name: installed"
  fi
}

check_cmd "git" "git" "" "apt-get install -y git"
check_cmd "tmux" "tmux" "" "apt-get install -y tmux"
check_cmd "jq" "jq" "" "apt-get install -y jq"
check_cmd "curl" "curl" "" "apt-get install -y curl"
check_cmd "gh" "gh" "" "apt-get install -y gh"
check_cmd "ripgrep" "rg" "" "apt-get install -y ripgrep"
check_cmd "htop" "htop" "" "apt-get install -y htop"
check_cmd "sqlite3" "sqlite3" "" "apt-get install -y sqlite3"
check_cmd "bc" "bc" "" "apt-get install -y bc"

# ── Node.js ─────────────────────────────────────────────────────────
echo ""
echo "Node.js"

if command -v node &>/dev/null; then
  NODE_VER=$(node --version 2>/dev/null)
  NODE_MAJOR="${NODE_VER#v}"
  NODE_MAJOR="${NODE_MAJOR%%.*}"
  if [[ "$NODE_MAJOR" -ge 22 ]]; then
    pass "Node.js: $NODE_VER"
  else
    warn "Node.js: $NODE_VER (v22+ recommended)"
  fi
else
  fail "Node.js: not installed"
  try_fix "curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && apt-get install -y nodejs" \
    && pass "Node.js: installed"
fi

if command -v npm &>/dev/null; then
  pass "npm: $(npm --version 2>/dev/null)"
else
  fail "npm: not found (should come with Node.js)"
fi

# ── Claude Code CLI ─────────────────────────────────────────────────
echo ""
echo "AI Agent CLIs"

if command -v claude &>/dev/null; then
  pass "Claude Code: $(claude --version 2>/dev/null | head -1)"
else
  fail "Claude Code: not installed"
  try_fix "npm install -g @anthropic-ai/claude-code" && pass "Claude Code: installed"
fi

if command -v copilot &>/dev/null; then
  pass "GitHub Copilot CLI: $(copilot --version 2>/dev/null | head -1)"
else
  warn "GitHub Copilot CLI: not installed (optional failover backend)"
fi

# ── Python ──────────────────────────────────────────────────────────
echo ""
echo "Python"

if command -v python3 &>/dev/null; then
  pass "Python3: $(python3 --version 2>/dev/null)"
else
  fail "Python3: not installed"
  try_fix "apt-get install -y python3 python3-venv" && pass "Python3: installed"
fi

# ── Go ──────────────────────────────────────────────────────────────
echo ""
echo "Go (optional — needed for Go-based projects)"

if command -v go &>/dev/null; then
  pass "Go: $(go version 2>/dev/null)"
else
  warn "Go: not installed (install if targeting Go projects)"
fi

# ── Docker ──────────────────────────────────────────────────────────
echo ""
echo "Docker (optional)"

if command -v docker &>/dev/null; then
  pass "Docker: $(docker --version 2>/dev/null)"
else
  warn "Docker: not installed (optional, for container-based workflows)"
fi

# ── Build Tools ─────────────────────────────────────────────────────
echo ""
echo "Build Tools"

check_cmd "make" "make" "" "apt-get install -y make"
check_cmd "gcc" "gcc" "" "apt-get install -y build-essential"

# ── Rust (needed for ai-native-storage-certus) ──────────────────────
echo ""
echo "Rust (needed for Rust-based projects)"

if command -v rustc &>/dev/null; then
  pass "Rust: $(rustc --version 2>/dev/null)"
else
  warn "Rust: not installed (needed for Rust projects like certus)"
fi

if command -v cargo &>/dev/null; then
  pass "Cargo: $(cargo --version 2>/dev/null)"
else
  warn "Cargo: not installed"
fi

# ── Hive Infrastructure ────────────────────────────────────────────
echo ""
echo "Hive Infrastructure"

if [[ -d /etc/hive ]]; then
  pass "/etc/hive directory exists"
else
  fail "/etc/hive directory does not exist"
  try_fix "mkdir -p /etc/hive" && pass "/etc/hive created"
fi

if [[ -d /var/run/hive ]]; then
  pass "/var/run/hive directory exists"
else
  fail "/var/run/hive directory does not exist"
  try_fix "mkdir -p /var/run/hive && chown dev:dev /var/run/hive" && pass "/var/run/hive created"
fi

if [[ -d /var/run/hive-metrics ]]; then
  pass "/var/run/hive-metrics directory exists"
else
  fail "/var/run/hive-metrics directory does not exist"
  try_fix "mkdir -p /var/run/hive-metrics && chown dev:dev /var/run/hive-metrics" \
    && pass "/var/run/hive-metrics created"
fi

for script in supervisor.sh agent-launch.sh hive-config.sh kick-agents.sh; do
  if [[ -x "/usr/local/bin/$script" ]]; then
    pass "$script installed"
  else
    fail "$script not found in /usr/local/bin/"
  fi
done

if [[ -f /usr/local/etc/hive/backends.conf ]]; then
  pass "backends.conf exists"
else
  fail "backends.conf not found at /usr/local/etc/hive/backends.conf"
fi

# ── Systemd Units ───────────────────────────────────────────────────
echo ""
echo "Systemd Units"

for unit in hive.service hive@.service; do
  if [[ -f "/etc/systemd/system/$unit" ]]; then
    pass "$unit installed"
  else
    fail "$unit not found"
  fi
done

# ── Git Configuration ──────────────────────────────────────────────
echo ""
echo "Git Configuration"

GIT_NAME=$(sudo -u dev git config --global user.name 2>/dev/null || echo "")
GIT_EMAIL=$(sudo -u dev git config --global user.email 2>/dev/null || echo "")
if [[ -n "$GIT_NAME" ]]; then
  pass "git user.name: $GIT_NAME"
else
  fail "git user.name not set for user 'dev'"
fi
if [[ -n "$GIT_EMAIL" ]]; then
  pass "git user.email: $GIT_EMAIL"
else
  fail "git user.email not set for user 'dev'"
fi

# ── GitHub Auth ─────────────────────────────────────────────────────
echo ""
echo "GitHub Authentication"

if sudo -u dev gh auth status &>/dev/null 2>&1; then
  pass "gh CLI authenticated"
else
  warn "gh CLI not authenticated (run: gh auth login)"
fi

if [[ -f /etc/hive/gh-app-key.pem ]]; then
  pass "GitHub App key exists"
else
  warn "GitHub App key not found at /etc/hive/gh-app-key.pem (needed for App-based auth)"
fi

# ── Notifications ───────────────────────────────────────────────────
echo ""
echo "Notifications (optional)"

if command -v ntfy &>/dev/null || [[ -f /usr/local/bin/ntfy ]]; then
  pass "ntfy: installed"
else
  warn "ntfy: not installed (optional push notifications)"
fi

# ── Summary ─────────────────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Results: $PASS passed, $FAIL failed, $WARN warnings"
if [[ "$FIXES_APPLIED" -gt 0 ]]; then
  echo "  Fixes applied: $FIXES_APPLIED"
fi
if [[ "$FAIL" -eq 0 ]]; then
  echo "  Status: READY ✓"
  exit 0
else
  echo "  Status: NOT READY — fix $FAIL issue(s) above"
  if ! $FIX_MODE; then
    echo "  Tip: re-run with --fix to auto-install missing packages"
  fi
  exit 1
fi
