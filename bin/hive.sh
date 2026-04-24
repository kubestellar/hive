#!/bin/bash
# hive — KubeStellar AI hive
#
# Usage:
#   hive supervisor   # bootstrap everything and start
#   hive status       # live dashboard
#   hive attach  [agent]
#   hive kick    [all|scanner|reviewer|architect|outreach]
#   hive logs    [governor|scanner|reviewer|architect|outreach|supervisor]
#   hive stop    [all|agent]

set -euo pipefail

HIVE_VERSION="0.2.0"
CONF="/etc/supervised-agent/hive.conf"
ENV_DIR="/etc/supervised-agent"
REPO_DIR="/tmp/supervised-agent"
LOG="/var/log/hive.log"

RED='\033[0;31m'; YLW='\033[1;33m'; GRN='\033[0;32m'
CYN='\033[0;36m'; BLD='\033[1m'; RST='\033[0m'

log()   { printf '%s %s\n' "$(date '+%T')" "$*" | tee -a "$LOG" 2>/dev/null || true; }
ok()    { echo -e "${GRN}✓${RST} $*";  log "OK   $*"; }
warn()  { echo -e "${YLW}⚠ $*${RST}"; log "WARN $*"; }
info()  { echo -e "${CYN}→ $*${RST}"; log "INFO $*"; }
fail()  { echo -e "${RED}✗ $*${RST}"; log "FAIL $*"; }
die()   { fail "$*"; exit 1; }
hdr()   { echo -e "\n${BLD}$*${RST}"; }

# ── load config ────────────────────────────────────────────────────

load_conf() {
  if [[ -f "$CONF" ]]; then
    # shellcheck disable=SC1090
    . "$CONF"
  fi
  # Defaults
  HIVE_REPOS="${HIVE_REPOS:-kubestellar/console kubestellar/kubestellar kubestellar/docs kubestellar/homebrew-tap kubestellar/console-kb}"
  NTFY_TOPIC="${NTFY_TOPIC:-}"
  NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"
  SLACK_WEBHOOK="${SLACK_WEBHOOK:-}"
  DISCORD_WEBHOOK="${DISCORD_WEBHOOK:-}"
  SUPERVISOR_CLI="${SUPERVISOR_CLI:-copilot}"
  SUPERVISOR_WORKDIR="${SUPERVISOR_WORKDIR:-/home/dev/kubestellar-console}"
  BEADS_SUPERVISOR_DIR="${BEADS_SUPERVISOR_DIR:-/home/dev/kubestellar-console}"
  BEADS_WORKER_DIR="${BEADS_WORKER_DIR:-/home/dev/scanner-beads}"
  AGENT_USER="${AGENT_USER:-dev}"
  HIVE_BACKENDS="${HIVE_BACKENDS:-copilot}"           # space-separated: copilot claude gemini goose
  HIVE_MODEL_SERVICES="${HIVE_MODEL_SERVICES:-}"      # space-separated: ollama litellm
  HIVE_TZ="${HIVE_TZ:-UTC}"                           # local timezone for status display
  HIVE_AUTO_INSTALL="${HIVE_AUTO_INSTALL:-true}"      # auto-install missing backends/services
}


usage() {
  echo -e "${BLD}hive ${HIVE_VERSION}${RST} — AI agent supervisor

${BLD}SETUP${RST}
  1. sudo apt install tmux
  2. curl -fsSL https://raw.githubusercontent.com/kubestellar/hive/main/install.sh | sudo bash
  3. sudo nano /etc/supervised-agent/hive.conf   # set NTFY_TOPIC, HIVE_REPOS, HIVE_BACKENDS
  4. hive supervisor

${BLD}COMMANDS${RST}
  supervisor                        Start everything: agents, governor, supervisor
  status                            Live dashboard
  attach  [agent]                   Watch an agent live  (Ctrl+B D to leave)
  kick    [all|scanner|reviewer|architect|outreach]
  logs    [governor|scanner|reviewer|architect|outreach|supervisor]
  stop    [all|agent]
  switch  <agent> <backend>            Switch agent to a different CLI backend

${BLD}BACKENDS${RST}  (set HIVE_BACKENDS in hive.conf)
  copilot   GitHub Copilot CLI (cloud)
  claude    Claude Code / Anthropic (cloud)
  gemini    Gemini CLI / Google (cloud)
  goose     Goose by Block (cloud or local via litellm)

${BLD}LOCAL MODELS${RST}  (set HIVE_MODEL_SERVICES=ollama litellm in hive.conf)
  ollama    Runs local models (llama3, codestral, qwen2.5-coder, ...)
  litellm   Unified proxy: routes goose → ollama or cloud APIs
"
  exit 0
}

# ── install missing tools ────────────────────────────────────────────────────

install_tools() {
  hdr "Checking tools"

  # curl (needed for everything below)
  if ! command -v curl &>/dev/null; then
    info "Installing curl..."
    sudo apt-get install -y curl &>/dev/null
    ok "curl installed"
  else
    ok "curl"
  fi

  # git
  if ! command -v git &>/dev/null; then
    info "Installing git..."
    sudo apt-get install -y git &>/dev/null
    ok "git installed"
  else
    ok "git $(git --version | awk '{print $3}')"
  fi

  # gh CLI
  if ! command -v gh &>/dev/null; then
    info "Installing gh CLI..."
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg 2>/dev/null
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
      https://cli.github.com/packages stable main" \
      | sudo tee /etc/apt/sources.list.d/github-cli.list &>/dev/null
    sudo apt-get update -qq && sudo apt-get install -y gh &>/dev/null
    ok "gh installed"
  else
    ok "gh $(gh --version 2>/dev/null | head -1 | awk '{print $3}')"
  fi

  # Node.js + npm — required for copilot / claude / gemini CLI backends
  local need_node=0
  for _b in $HIVE_BACKENDS; do
    case "$_b" in copilot|claude|gemini) need_node=1; break;; esac
  done
  if [[ $need_node -eq 1 ]] && ! command -v npm &>/dev/null; then
    info "Installing Node.js (LTS) via NodeSource..."
    curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo bash - &>/dev/null
    sudo apt-get install -y nodejs &>/dev/null
    ok "node $(node --version) / npm $(npm --version)"
  elif command -v npm &>/dev/null; then
    ok "node $(node --version 2>/dev/null) / npm $(npm --version 2>/dev/null)"
  fi

  # python3 — required for litellm
  local need_python=0
  for _s in $HIVE_MODEL_SERVICES; do
    [[ "$_s" == "litellm" ]] && need_python=1 && break
  done
  if [[ $need_python -eq 1 ]] && ! command -v python3 &>/dev/null; then
    info "Installing python3..."
    sudo apt-get install -y python3 python3-pip &>/dev/null
    ok "python3 installed"
  elif command -v python3 &>/dev/null; then
    ok "python3 $(python3 --version 2>&1 | awk '{print $2}')"
  fi

  # bd (beads)
  if ! command -v bd &>/dev/null; then
    info "Installing bd (beads)..."
    if [[ -f "$REPO_DIR/bin/install-bd.sh" ]]; then
      bash "$REPO_DIR/bin/install-bd.sh"
    else
      warn "bd not found — install manually from https://github.com/upward-labs/beads"
    fi
  else
    ok "bd (beads)"
  fi

  # ── Backend CLIs ──────────────────────────────────────────────────────────
  if [[ "${HIVE_AUTO_INSTALL:-true}" == "true" ]]; then
    for backend in $HIVE_BACKENDS; do
      if command -v "$backend" &>/dev/null; then
        ok "$backend CLI"
        continue
      fi
      info "Installing $backend CLI..."
      case "$backend" in
        copilot)
          npm i -g @github/copilot-cli &>/dev/null && ok "copilot installed" \
            || warn "copilot install failed — run: npm i -g @github/copilot-cli"
          ;;
        claude)
          npm i -g @anthropic-ai/claude-code &>/dev/null && ok "claude installed" \
            || warn "claude install failed — run: npm i -g @anthropic-ai/claude-code"
          ;;
        gemini)
          npm i -g @google/gemini-cli &>/dev/null && ok "gemini installed" \
            || warn "gemini install failed — run: npm i -g @google/gemini-cli"
          ;;
        goose)
          curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh \
            | bash &>/dev/null && ok "goose installed" \
            || warn "goose install failed — see https://github.com/block/goose"
          ;;
        *)
          warn "Unknown backend '$backend' — skipping auto-install"
          ;;
      esac
    done
  else
    # HIVE_AUTO_INSTALL=false — just verify they exist
    for backend in $HIVE_BACKENDS; do
      if ! command -v "$backend" &>/dev/null; then
        die "$backend CLI not found. Set HIVE_AUTO_INSTALL=true or install manually."
      fi
      ok "$backend CLI"
    done
  fi

  # ── Model services (optional) ────────────────────────────────────────────
  if [[ "${HIVE_AUTO_INSTALL:-true}" == "true" ]]; then
    for svc in ${HIVE_MODEL_SERVICES:-}; do
      case "$svc" in
        ollama)
          if ! command -v ollama &>/dev/null; then
            info "Installing ollama..."
            curl -fsSL https://ollama.ai/install.sh | bash &>/dev/null \
              && ok "ollama installed" \
              || warn "ollama install failed — see https://ollama.ai"
          else
            ok "ollama $(ollama --version 2>/dev/null || echo '')"
          fi
          ;;
        litellm)
          if ! python3 -c "import litellm" &>/dev/null 2>&1; then
            info "Installing litellm[proxy]..."
            pip install litellm[proxy] -q \
              && ok "litellm installed" \
              || warn "litellm install failed — run: pip install litellm[proxy]"
          else
            ok "litellm $(python3 -c 'import litellm; print(litellm.__version__)' 2>/dev/null || echo '')"
          fi
          ;;
        *)
          warn "Unknown model service '$svc' — skipping"
          ;;
      esac
    done
  fi

  # gh auth
  if ! gh auth status &>/dev/null 2>&1; then
    echo ""
    warn "gh not authenticated"
    echo "  Run: gh auth login"
    die "Cannot continue without gh auth"
  fi
  ok "gh authenticated"
}

# ── validate config ──────────────────────────────────────────────────────────

validate_conf() {
  hdr "Config ($CONF)"

  local any_notify=0
  if [[ -n "$NTFY_TOPIC" ]];      then ok "ntfy → ${NTFY_SERVER}/${NTFY_TOPIC}"; any_notify=1; fi
  if [[ -n "$SLACK_WEBHOOK" ]];   then ok "Slack webhook configured"; any_notify=1; fi
  if [[ -n "$DISCORD_WEBHOOK" ]]; then ok "Discord webhook configured"; any_notify=1; fi
  if [[ $any_notify -eq 0 ]]; then
    warn "No notification channels configured — alerts disabled"
    echo "  Set NTFY_TOPIC, SLACK_WEBHOOK, or DISCORD_WEBHOOK in $CONF"
  fi

  ok "HIVE_REPOS=${HIVE_REPOS}"
  ok "SUPERVISOR_CLI=${SUPERVISOR_CLI}"

  # Write HIVE_REPOS + NTFY_TOPIC into governor.env so kick-governor picks them up
  if [[ -f "$ENV_DIR/governor.env" ]]; then
    if ! grep -q "^HIVE_REPOS=" "$ENV_DIR/governor.env" 2>/dev/null; then
      echo "HIVE_REPOS=\"${HIVE_REPOS}\"" | sudo tee -a "$ENV_DIR/governor.env" &>/dev/null
    else
      sudo sed -i "s|^HIVE_REPOS=.*|HIVE_REPOS=\"${HIVE_REPOS}\"|" "$ENV_DIR/governor.env"
    fi
    if [[ -n "$NTFY_TOPIC" ]]; then
      sudo sed -i "s|^NTFY_TOPIC=.*|NTFY_TOPIC=${NTFY_TOPIC}|" "$ENV_DIR/governor.env"
    fi
    ok "governor.env updated"
  fi
}

# ── init beads databases ─────────────────────────────────────────────────────

init_beads() {
  hdr "Beads"

  for bdir in "$BEADS_SUPERVISOR_DIR" "$BEADS_WORKER_DIR"; do
    if [[ ! -d "$bdir" ]]; then
      warn "Beads dir missing: $bdir"
      continue
    fi
    if ! (cd "$bdir" && bd list &>/dev/null 2>&1); then
      info "Initialising beads in $bdir..."
      (cd "$bdir" && bd init 2>/dev/null) && ok "Beads initialised in $bdir" || warn "bd init failed in $bdir"
    else
      local count
      count=$(cd "$bdir" && bd list --json 2>/dev/null \
        | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d.get("items",[])))' 2>/dev/null || echo '?')
      ok "$bdir ($count items)"
    fi
  done
}

# ── clone / refresh repo hive ───────────────────────────

ensure_repo() {
  hdr "Hive repo"
  if [[ -d "$REPO_DIR/.git" ]]; then
    cd "$REPO_DIR" && git pull --ff-only -q 2>/dev/null \
      && ok "Repo current ($REPO_DIR)" \
      || warn "Could not pull — using cached version"
  else
    info "Cloning kubestellar/hive → $REPO_DIR..."
    git clone --depth=1 https://github.com/kubestellar/hive.git "$REPO_DIR" -q 2>/dev/null \
      && ok "Repo cloned" \
      || warn "Clone failed — continuing without fresh repo"
  fi
}

# ── start agent watchdog services ────────────────────────────────────────────

ensure_agents() {
  hdr "Agent services"

  local services=(
    "claude-scanner.service:scanner"
    "supervised-agent@reviewer.service:reviewer"
    "supervised-agent@feature.service:architect"
    "supervised-agent@outreach.service:outreach"
  )
  local all_units=("${services[@]}" "kick-governor.timer:governor")

  # self-heal: run install.sh if any unit files are missing
  local needs_install=0
  for entry in "${all_units[@]}"; do
    local svc="${entry%%:*}"
    if ! systemctl list-unit-files "$svc" 2>/dev/null | grep -q "^${svc}"; then
      warn "Unit missing: $svc"
      needs_install=1
    fi
  done

  if [[ $needs_install -eq 1 ]]; then
    if [[ -f "$REPO_DIR/install.sh" ]]; then
      info "Running install.sh to register systemd units..."
      sudo bash "$REPO_DIR/install.sh" \
        && ok "install.sh completed" \
        || die "install.sh failed — check output above"
      sudo systemctl daemon-reload
    else
      die "Units missing and $REPO_DIR/install.sh not found. Run: git clone https://github.com/kubestellar/hive $REPO_DIR"
    fi
  fi

  # start (or confirm) each service
  for entry in "${services[@]}"; do
    local svc="${entry%%:*}" label="${entry##*:}"
    local state
    state=$(systemctl is-active "$svc" 2>/dev/null || echo "inactive")
    if [[ "$state" == "active" ]]; then
      ok "$label ($svc)"
    else
      info "Starting $label..."
      sudo systemctl start "$svc" 2>/dev/null \
        && ok "$label started" \
        || warn "$label could not start — check: journalctl -u $svc"
    fi
  done

  # governor timer
  local gt
  gt=$(systemctl is-active kick-governor.timer 2>/dev/null || echo "inactive")
  if [[ "$gt" == "active" ]]; then
    ok "kick-governor.timer"
  else
    info "Starting kick-governor.timer..."
    sudo systemctl enable --now kick-governor.timer 2>/dev/null \
      && ok "kick-governor.timer started" \
      || warn "kick-governor.timer could not start"
  fi
}
# ── initial kick to all agents ───────────────────────────────────────────────

kick_agents() {
  hdr "Kicking agents"
  sleep 4  # let sessions initialise

  local msg="STARTUP: read your beads (cd $BEADS_WORKER_DIR && bd list --json). Resume any in_progress item. For new work: bd ready --json. Read your policy file from memory. Report status."

  for session in issue-scanner reviewer feature outreach; do
    if tmux has-session -t "$session" 2>/dev/null; then
      tmux send-keys -t "$session" "$msg" 2>/dev/null || true
      sleep 0.3
      tmux send-keys -t "$session" Enter 2>/dev/null || true
      ok "$session kicked"
    else
      warn "$session session not ready yet — governor will kick on next cycle"
    fi
  done
}

# ── start supervisor session ─

start_supervisor() {
  local cli="${1:-$SUPERVISOR_CLI}"

  hdr "Supervisor session ($cli)"

  if tmux has-session -t supervisor 2>/dev/null; then
    ok "Supervisor already running"
    echo ""
    echo -e "  ${BLD}Watch it:${RST}  hive attach supervisor"
    return 0
  fi

  local launch_cmd
  case "$cli" in
    copilot) launch_cmd="/usr/bin/copilot --allow-all" ;;
    claude)  launch_cmd="/usr/bin/claude --dangerously-skip-permissions" ;;
    *)       die "Unknown CLI: $cli. Use --copilot or --claude" ;;
  esac

  local ready_marker
  case "$cli" in
    copilot) ready_marker="Environment loaded" ;;
    claude)  ready_marker="bypass permissions on" ;;
  esac

  # Load supervisor loop prompt
  local loop_prompt=""
  [[ -f "$ENV_DIR/supervisor.env" ]] && . "$ENV_DIR/supervisor.env"
  loop_prompt="${AGENT_LOOP_PROMPT:-You are the Supervisor. Read the hive repo at $REPO_DIR. Check all agent sessions. Triage issues. Dispatch work orders. Monitor PRs. Report status.}"

  info "Creating supervisor tmux session..."
  tmux new-session -d -s supervisor -c "$SUPERVISOR_WORKDIR"

  info "Launching $cli..."
  tmux send-keys -t supervisor "$launch_cmd"
  sleep 0.3
  tmux send-keys -t supervisor Enter

  info "Waiting for ready..."
  local i=0
  while (( i < 60 )); do
    tmux capture-pane -t supervisor -p 2>/dev/null | grep -qF "$ready_marker" && break
    sleep 2; (( i++ )) || true
  done
  (( i < 60 )) && ok "$cli ready" || warn "Ready marker not seen — injecting prompt anyway"

  sleep 1
  tmux send-keys -t supervisor "$loop_prompt"
  sleep 0.3
  tmux send-keys -t supervisor Enter

  ok "Supervisor running"
  echo ""
  echo -e "  ${BLD}Watch it:${RST}  hive attach supervisor"
}

# ── status dashboard ─────────────────────

cmd_status() {
  echo -e "\n${BLD}🐝 hive status — $(date '+%H:%M:%S %Z')${RST}\n"

  # Sessions
  local SESSIONS=(supervisor issue-scanner reviewer feature outreach)
  local LABELS=("supervisor" "scanner" "reviewer" "architect" "outreach")
  local ENV_FILES=("supervisor" "issue-scanner" "reviewer" "feature" "outreach")
  printf "  %-12s  %-8s  %-8s  %s\n" "AGENT" "STATE" "CLI" "LAST LINE"
  printf "  %-12s  %-8s  %-8s  %s\n" "-----" "-----" "---" "---------"
  for i in "${!SESSIONS[@]}"; do
    local s="${SESSIONS[$i]}" label="${LABELS[$i]}"
    local cli
    if tmux has-session -t "$s" 2>/dev/null; then
      local pane line
      pane=$(tmux capture-pane -t "$s" -p 2>/dev/null || echo "")
      # Detect actual running CLI from last 5 lines of pane only
      local pane_tail
      pane_tail=$(echo "$pane" | tail -5)
      if echo "$pane_tail" | grep -q "bypass permissions\|claude doctor\|Claude Code v"; then
        cli="claude"
      elif echo "$pane_tail" | grep -q "ctrl+q enqueue\|/ commands.*help"; then
        cli="copilot"
      else
        cli=$(grep "^AGENT_CLI=" "$ENV_DIR/${ENV_FILES[$i]}.env" 2>/dev/null | cut -d= -f2 | tr -d '"' || echo "?")
      fi
      line=$(echo "$pane" | grep -v '^$' | tail -1 | cut -c1-55 || echo "")
      printf "  ${GRN}%-12s${RST}  %-8s  %-8s  ${CYN}%s${RST}\n" "$label" "running" "$cli" "$line"
    else
      cli=$(grep "^AGENT_CLI=" "$ENV_DIR/${ENV_FILES[$i]}.env" 2>/dev/null | cut -d= -f2 | tr -d '"' || echo "?")
      printf "  ${RED}%-12s${RST}  %-8s  %-8s\n" "$label" "stopped" "$cli"
    fi
  done

  # Governor
  echo ""
  local mode busy_pct
  mode=$(    cat /var/run/kick-governor/mode         2>/dev/null || echo "unknown")
  busy_pct=$(cat /var/run/kick-governor/busyness_pct 2>/dev/null || echo "?")
  local next
  next=$(systemctl list-timers kick-governor.timer --no-pager 2>/dev/null \
       | awk 'NR==2{print $1,$2,$3}' \
       | xargs -I{} bash -c "TZ=\"$HIVE_TZ\" date -d \"{}\" \"+%-I:%M %p %Z\"" 2>/dev/null || echo "unknown")
  echo -e "  Governor:  ${BLD}$mode${RST}  ${busy_pct}% busy  |  next kick: ${CYN}$next${RST}"

  # Beads
  echo ""
  local wc sc
  wc=$(cd "$BEADS_WORKER_DIR"     2>/dev/null && bd list --json 2>/dev/null \
     | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d) if isinstance(d,list) else len(d.get("items",[])))' 2>/dev/null || echo '?')
  sc=$(cd "$BEADS_SUPERVISOR_DIR" 2>/dev/null && bd list --json 2>/dev/null \
     | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d) if isinstance(d,list) else len(d.get("items",[])))' 2>/dev/null || echo '?')
  echo -e "  Beads:     workers ${BLD}$wc${RST}  |  supervisor ${BLD}$sc${RST}"
  echo ""
}

# ── attach ───────────────────────────────────────────────────────────────────

cmd_attach() {
  local name="${1:-supervisor}"
  # map friendly names to session names
  case "$name" in
    scanner|issue-scanner) name="issue-scanner" ;;
    architect|feature)     name="feature" ;;
    supervisor|reviewer|outreach) ;;
    *) die "Unknown agent: $name. Use: supervisor scanner reviewer architect outreach" ;;
  esac

  if ! tmux has-session -t "$name" 2>/dev/null; then
    die "$name is not running. Start it with: hive supervisor"
  fi

  echo -e "${CYN}Attaching to ${BLD}$name${RST}${CYN} — press Ctrl+B then D to detach${RST}"
  exec tmux attach -t "$name"
}

# ── logs ─────────────────────────────────────────────────────────────────────

cmd_logs() {
  case "${1:-governor}" in
    governor)           exec journalctl -u kick-governor -f --no-pager ;;
    scanner)            exec journalctl -u claude-scanner -f --no-pager ;;
    reviewer)           exec journalctl -u "supervised-agent@reviewer" -f --no-pager ;;
    architect|feature)  exec journalctl -u "supervised-agent@feature" -f --no-pager ;;
    outreach)           exec journalctl -u "supervised-agent@outreach" -f --no-pager ;;
    supervisor)         exec journalctl -u "supervised-agent@supervisor" -f --no-pager ;;
    *) die "Unknown agent. Use: governor scanner reviewer architect outreach supervisor" ;;
  esac
}


cmd_kick() {
  exec /usr/local/bin/kick-agents.sh "${1:-all}"
}

# ── stop ─────────────────────────────────────────────────────────────────────

cmd_stop() {
  local target="${1:-all}"
  if [[ "$target" == "all" ]]; then
    info "Stopping all agents..."
    for svc in claude-scanner supervised-agent@reviewer supervised-agent@feature supervised-agent@outreach supervised-agent@supervisor; do
      sudo systemctl stop "$svc" 2>/dev/null && ok "Stopped $svc" || true
    done
    sudo systemctl stop kick-governor.timer 2>/dev/null && ok "Stopped governor" || true
  else
    case "$target" in
      scanner)  sudo systemctl stop claude-scanner ;;
      reviewer) sudo systemctl stop "supervised-agent@reviewer" ;;
      architect|feature) sudo systemctl stop "supervised-agent@feature" ;;
      outreach) sudo systemctl stop "supervised-agent@outreach" ;;
      supervisor) sudo systemctl stop "supervised-agent@supervisor" ;;
      *) die "Unknown agent: $target" ;;
    esac
    ok "Stopped $target"
  fi
}

# ── switch ───────────────────────────────────────────────────────────────────

cmd_switch() {
  local agent="${1:-}" backend="${2:-}"
  [[ -z "$agent" || -z "$backend" ]] && die "Usage: hive switch <agent> <backend>  (backends: copilot claude gemini goose)"

  # Map agent name → session name and env file
  local session envfile
  case "$agent" in
    scanner)            session="issue-scanner"; envfile="issue-scanner" ;;
    reviewer)           session="reviewer";      envfile="reviewer" ;;
    architect|feature)  session="feature";       envfile="feature" ;;
    outreach)           session="outreach";      envfile="outreach" ;;
    supervisor)         session="supervisor";    envfile="supervisor" ;;
    *) die "Unknown agent: $agent (valid: scanner reviewer architect outreach supervisor)" ;;
  esac

  # Resolve launch command for backend
  local launch_cmd
  case "$backend" in
    copilot) launch_cmd="/usr/bin/copilot --allow-all --model claude-opus-4.6" ;;
    claude)  launch_cmd="/usr/bin/claude --dangerously-skip-permissions --model claude-opus-4-7" ;;
    gemini)  launch_cmd="/usr/bin/gemini --yolo" ;;
    goose)   launch_cmd="/usr/bin/goose --no-confirm" ;;
    *) die "Unknown backend: $backend (valid: copilot claude gemini goose)" ;;
  esac

  info "Switching $agent → $backend"

  # Update env file
  local ef="$ENV_DIR/${envfile}.env"
  if [[ -f "$ef" ]]; then
    sudo sed -i "s|^AGENT_LAUNCH_CMD=.*|AGENT_LAUNCH_CMD=\"${launch_cmd}\"|" "$ef"
    sudo sed -i "s|^AGENT_CLI=.*|AGENT_CLI=${backend}|" "$ef"
    ok "Updated $ef"
  else
    die "Env file not found: $ef"
  fi

  # Kill existing session
  if tmux has-session -t "$session" 2>/dev/null; then
    tmux kill-session -t "$session" 2>/dev/null && ok "Killed session $session"
  fi

  # Respawn via kick
  sleep 1
  /usr/local/bin/kick-agents.sh "$agent" 2>/dev/null || true
  ok "Kicked $agent — will start with $backend"
  sleep 3
  cmd_status
}

# ── main ─────────────────────────────────────────────────────────────────────

main() {
  [[ $# -eq 0 ]] && usage

  load_conf

  case "$1" in
    supervisor)
      shift
      # parse --copilot / --claude
      local cli="$SUPERVISOR_CLI"
      while [[ $# -gt 0 ]]; do
        case "$1" in
          --copilot) cli="copilot" ;;
          --claude)  cli="claude" ;;
          -h|--help) usage ;;
          *) die "Unknown flag: $1" ;;
        esac
        shift
      done

      echo -e "\n${BLD}🐝 hive ${HIVE_VERSION} — starting ($cli)${RST}"

      install_tools
      validate_conf
      init_beads
      ensure_repo
      ensure_agents
      kick_agents
      start_supervisor "$cli"

      printf '%b\xf0\x9f\x90\x9d Hive is running.%b\n' "${GRN}${BLD}" "$RST"
      cmd_status
      ;;

    status)   cmd_status ;;
    attach)   shift; cmd_attach  "${1:-supervisor}" ;;
    kick)     shift; cmd_kick    "${1:-all}" ;;
    logs)     shift; cmd_logs    "${1:-governor}" ;;
    stop)     shift; cmd_stop    "${1:-all}" ;;
    switch)   shift; cmd_switch  "$@" ;;
    -h|--help|help) usage ;;
    *) fail "Unknown command: $1"; usage ;;
  esac
}

main "$@"
