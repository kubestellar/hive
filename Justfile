# Justfile — KubeStellar Hive contributor commands
#
# Install just: brew install just (macOS) or cargo install just
# Usage: just contribute-setup claude && just contribute-hive

set shell := ["bash", "-euo", "pipefail", "-c"]

hive_image := env("HIVE_CONTRIBUTOR_IMAGE", "ghcr.io/kubestellar/hive-contributor:latest")
hive_hub := env("HIVE_HUB", "wss://hive.kubestellar.io:3001/contribute")
config_dir := env("HOME") + "/.config/hive"

# Show available commands
default:
    @just --list

# One-time setup: register with hub + authenticate GitHub + authenticate CLI
contribute-setup backend="claude":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{config_dir}}"
    echo "=== Hive Contributor Setup ==="
    echo ""

    # ── Step 1: GitHub authentication ──
    echo "── Step 1/3: GitHub Authentication ──"
    if ! command -v gh &>/dev/null; then
      echo "ERROR: gh CLI not found. Install: brew install gh"
      exit 1
    fi
    if gh auth status &>/dev/null; then
      GH_USER=$(gh api user --jq '.login' 2>/dev/null || echo "")
      echo "Already authenticated as: ${GH_USER}"
    else
      echo "Logging into GitHub..."
      gh auth login --web --scopes "repo,read:org"
      GH_USER=$(gh api user --jq '.login' 2>/dev/null || echo "")
      echo "Authenticated as: ${GH_USER}"
    fi
    GH_TOKEN=$(gh auth token 2>/dev/null || echo "")
    if [[ -n "$GH_TOKEN" ]]; then
      echo "GH_TOKEN=${GH_TOKEN}" > "{{config_dir}}/gh-auth.env"
      chmod 600 "{{config_dir}}/gh-auth.env"
    fi
    echo ""

    # ── Step 2: Register with hive hub ──
    echo "── Step 2/3: Hive Registration ──"
    HUB_HTTP=$(echo "{{hive_hub}}" | sed 's|^wss://|https://|;s|^ws://|http://|;s|/contribute$||')
    RESPONSE=$(curl -sf -X POST "${HUB_HTTP}/api/contribute/register" \
      -H "Content-Type: application/json" \
      -d "{\"github_username\": \"${GH_USER}\"}" 2>&1) || {
        echo "ERROR: Registration failed. Is the hub running at ${HUB_HTTP}?"
        exit 1
    }
    TOKEN=$(echo "$RESPONSE" | jq -r '.registration_token')
    CID=$(echo "$RESPONSE" | jq -r '.contributor_id')
    MSG=$(echo "$RESPONSE" | jq -r '.message')
    if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
      if echo "$MSG" | grep -qi "already registered"; then
        if [[ -f "{{config_dir}}/contributor.env" ]]; then
          source "{{config_dir}}/contributor.env"
          echo "Already registered — ${GH_USER} (${CONTRIBUTOR_ID:-unknown})"
        else
          echo "ERROR: Already registered but no local config found."
          exit 1
        fi
      else
        echo "ERROR: ${MSG:-No token received}"
        exit 1
      fi
    cat > "{{config_dir}}/contributor.env" <<EOF
    HIVE_REGISTRATION_TOKEN=${TOKEN}
    HIVE_HUB={{hive_hub}}
    CONTRIBUTOR_ID=${CID}
    CONTRIBUTOR_USERNAME=${GH_USER}
    AGENT_BACKEND={{backend}}
    EOF
    echo "${MSG} — ${GH_USER} (${CID})"
    echo ""

    # ── Step 3: CLI authentication ──
    echo "── Step 3/3: {{backend}} CLI Authentication ──"
    case "{{backend}}" in
      claude)
        if ! command -v claude &>/dev/null; then
          echo "ERROR: Claude Code not installed. Install: npm i -g @anthropic-ai/claude-code"
          exit 1
        fi
        if [[ -d "${HOME}/.claude" ]] || [[ -d "${HOME}/.config/claude-code" ]]; then
          echo "Claude Code authenticated (credentials found)"
        else
          echo "Claude Code needs authentication. Opening Claude Code..."
          echo "Type /login then exit when done (Ctrl+C or type /exit)."
          echo ""
          claude -p "/login"
          echo "Claude Code authentication complete."
        fi
        ;;
      copilot)
        if command -v copilot &>/dev/null || command -v gh &>/dev/null; then
          echo "Copilot uses your gh auth — already authenticated."
        else
          echo "ERROR: Install copilot: gh extension install github/gh-copilot"
          exit 1
        fi
        ;;
      gemini)
        if command -v gemini &>/dev/null; then
          gemini auth login 2>/dev/null || echo "Gemini login complete (or already authenticated)"
        else
          echo "ERROR: Gemini CLI not installed."
          exit 1
        fi
        ;;
      bob)
        if command -v bob &>/dev/null; then
          echo "Bob CLI detected — authentication handled on first run."
        else
          echo "ERROR: Bob CLI not found."
          exit 1
        fi
        ;;
      goose)
        if command -v goose &>/dev/null; then
          echo "Goose CLI detected — authentication handled on first run."
        else
          echo "ERROR: Goose CLI not found."
          exit 1
        fi
        ;;
      *)
        echo "ERROR: Unknown backend '{{backend}}'. Supported: claude, copilot, gemini, bob, goose"
        exit 1
        ;;
    esac

    # Copy CLI config for Docker container (Colima can't bind-mount files)
    if [[ "{{backend}}" == "claude" ]] && [[ -f "${HOME}/.claude.json" ]]; then
      cp "${HOME}/.claude.json" "{{config_dir}}/claude-config.json"
      chmod 600 "{{config_dir}}/claude-config.json"
      echo "Claude config staged for Docker container."
    fi

    echo ""
    echo "✓ Setup complete!"
    echo "  GitHub:  ${GH_USER}"
    echo "  CLI:     {{backend}}"
    echo "  Hub:     {{hive_hub}}"
    echo ""
    echo "Run 'just contribute-hive' to start contributing."

# Start contributing — Docker (default) or local mode
# Usage: just contribute-hive        (Docker, reads CLI from contributor.env)
#        just contribute-hive local   (native, starts relay + CLI directly)
# Start contributing — Docker (default) or local mode
# Usage: just contribute-hive              (Docker, default CLI from setup)
#        just contribute-hive copilot      (Docker, copilot backend)
#        just contribute-hive claude local  (native mode, claude)
contribute-hive backend="" mode="docker":
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! -f "{{config_dir}}/contributor.env" ]]; then
      echo "Not set up yet. Run: just contribute-setup <cli>"
      exit 1
    fi
    if [[ ! -f "{{config_dir}}/gh-auth.env" ]]; then
      echo "Not set up yet. Run: just contribute-setup <cli>"
      exit 1
    fi
    source "{{config_dir}}/gh-auth.env"
    source "{{config_dir}}/contributor.env"
    # Handle "just contribute-hive local" (backward compat)
    _BACKEND="{{backend}}"
    _MODE="{{mode}}"
    if [[ "$_BACKEND" == "local" || "$_BACKEND" == "docker" ]]; then
      _MODE="$_BACKEND"
      _BACKEND=""
    fi
    if [[ -n "$_BACKEND" ]]; then
      BACKEND="$_BACKEND"
    else
      BACKEND="${AGENT_BACKEND:-claude}"
    fi
    echo "=== Hive Contributor Agent ==="
    echo "Backend:  ${BACKEND}"
    echo "Hub:      {{hive_hub}}"
    echo "GitHub:   $(gh api user --jq '.login' 2>/dev/null || echo 'authenticated')"
    echo ""

    if [[ "$_MODE" == "local" ]]; then
      # ── Local mode: start node relay in background + launch CLI directly ──
      HUB="${HIVE_HUB:-{{hive_hub}}}"
      WS_URL=$(echo "$HUB" | sed 's|/contribute/*$|/api/contribute/ws|')
      echo "Starting local relay..."
      echo "WebSocket: ${WS_URL}"
      RELAY_PID=""
      if [[ -f "v2/proxy/relay.js" ]]; then
        HIVE_WS_URL="${WS_URL}" node v2/proxy/relay.js &
        RELAY_PID=$!
        trap "kill ${RELAY_PID} 2>/dev/null || true" EXIT
        sleep 1
      fi
      echo "Launching ${BACKEND} CLI (interactive)..."
      case "${BACKEND}" in
        claude)  claude --dangerously-skip-permissions ;;
        copilot) copilot ;;
        bob)     bob --accept-license ;;
        gemini)  gemini --yolo ;;
        goose)   goose --no-confirm ;;
        *)
          echo "ERROR: Unknown backend '${BACKEND}'"
          exit 1
          ;;
      esac
    else
      # ── Docker mode: stop existing, start fresh ──
      if [[ "${HIVE_SKIP_PULL:-}" != "true" ]]; then
        echo "Pulling {{hive_image}}..."
        docker pull {{hive_image}} 2>/dev/null || echo "Pull failed — using local image"
        echo ""
      fi
      # Mount CLI-specific config directories
      CLI_MOUNTS=""
      case "${BACKEND}" in
        claude)
          CLI_MOUNTS="-v ${HOME}/.claude:/home/dev/.claude -v ${HOME}/.config/claude-code:/home/dev/.config/claude-code"
          ;;
        copilot)
          [ -d "${HOME}/.copilot" ] && CLI_MOUNTS="-v ${HOME}/.copilot:/home/dev/.copilot"
          ;;
        gemini)
          [ -d "${HOME}/.gemini" ] && CLI_MOUNTS="-v ${HOME}/.gemini:/home/dev/.gemini"
          [ -d "${HOME}/.config/gemini" ] && CLI_MOUNTS="${CLI_MOUNTS} -v ${HOME}/.config/gemini:/home/dev/.config/gemini"
          ;;
        goose)
          [ -d "${HOME}/.config/goose" ] && CLI_MOUNTS="-v ${HOME}/.config/goose:/home/dev/.config/goose"
          ;;
      esac
      CONTAINER_NAME="hive-contributor-${BACKEND}-$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' ')"
      docker run -it --rm \
        --name "${CONTAINER_NAME}" \
        --network host \
        -v "{{config_dir}}:/home/dev/.config/hive:ro" \
        ${CLI_MOUNTS} \
        -v "${HOME}/.config/gh:/home/dev/.config/gh:ro" \
        -e HIVE_HUB="{{hive_hub}}" \
        -e AGENT_BACKEND="${BACKEND}" \
        -e GH_TOKEN="${GH_TOKEN}" \
        -e HIVE_USE_CONTRIBUTOR_GH=true \
        -e HIVE_CONTAINER_NAME="${CONTAINER_NAME}" \
        ${ANTHROPIC_API_KEY:+-e ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY}"} \
        ${ANTHROPIC_API_KEY:+-e ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY}"} \
        ${GOOGLE_API_KEY:+-e GOOGLE_API_KEY="${GOOGLE_API_KEY}"} \
        ${GOOSE_API_KEY:+-e GOOSE_API_KEY="${GOOSE_API_KEY}"} \
        ${GOOSE_PROVIDER:+-e GOOSE_PROVIDER="${GOOSE_PROVIDER}"} \
        ${GOOSE_MODEL:+-e GOOSE_MODEL="${GOOSE_MODEL}"} \
        ${OPENAI_API_KEY:+-e OPENAI_API_KEY="${OPENAI_API_KEY}"} \
        {{hive_image}}
    fi

# Check hub status and your contributor profile
contribute-status:
    #!/usr/bin/env bash
    set -euo pipefail
    HUB_HTTP=$(echo "{{hive_hub}}" | sed 's|^wss://|https://|;s|^ws://|http://|;s|/contribute$||')
    echo "=== Hub Status ==="
    curl -sf "${HUB_HTTP}/api/contribute/status" 2>/dev/null | jq . || echo "Hub unreachable at ${HUB_HTTP}"
    if [[ -f "{{config_dir}}/contributor.env" ]]; then
      source "{{config_dir}}/contributor.env"
      echo ""
      echo "=== Your Profile ==="
      curl -sf "${HUB_HTTP}/api/contributors/${CONTRIBUTOR_ID}" 2>/dev/null | jq . || echo "Could not fetch profile"
    fi

# Browse available Hive projects to contribute to
contribute-browse:
    #!/usr/bin/env bash
    set -euo pipefail
    HUB_HTTP=$(echo "{{hive_hub}}" | sed 's|^wss://|https://|;s|^ws://|http://|;s|/contribute$||')
    echo "=== Available Hives ==="
    echo ""
    curl -sf "${HUB_HTTP}/api/hives" 2>/dev/null | jq -r '.hives[] | "  \(.project_name) (\(.org))\n    Hub: \(.hub_url)\n    Dashboard: \(.dashboard_url // "N/A")\n    Contributors: \(.active_contributors // 0) active\n    Actionable: \(.actionable_items // "?") items\n"' || echo "Could not reach registry at ${HUB_HTTP}"

# Call the authenticated hive API
# Usage: just hive-api /status
#        just hive-api /me
#        just hive-api /contributors
#        just hive-api /activity
#        just hive-api /knowledge
hive-api endpoint="/status":
    #!/usr/bin/env bash
    set -euo pipefail
    HUB_HTTP=$(echo "{{hive_hub}}" | sed 's|^wss://|https://|;s|^ws://|http://|;s|/contribute$||')
    TOKEN=$(gh auth token 2>/dev/null || echo "")
    if [[ -z "$TOKEN" ]]; then
      echo "ERROR: Not authenticated. Run: gh auth login"
      exit 1
    fi
    ENDPOINT="{{endpoint}}"
    [[ "$ENDPOINT" != /* ]] && ENDPOINT="/$ENDPOINT"
    curl -sf -H "Authorization: Bearer ${TOKEN}" "${HUB_HTTP}/api/v1${ENDPOINT}" 2>&1 | python3 -m json.tool 2>/dev/null || curl -sf -H "Authorization: Bearer ${TOKEN}" "${HUB_HTTP}/api/v1${ENDPOINT}" 2>&1
    echo ""

# Open the API docs in your browser
hive-api-docs:
    #!/usr/bin/env bash
    HUB_HTTP=$(echo "{{hive_hub}}" | sed 's|^wss://|https://|;s|^ws://|http://|;s|/contribute$||')
    open "${HUB_HTTP}/api/docs" 2>/dev/null || echo "Visit: ${HUB_HTTP}/api/docs"

# Stop contributing (if running in background)
contribute-stop:
    @docker ps --filter "name=hive-contributor-" --format '{{ '{{' }}.Names{{ '}}' }}' | xargs -r docker stop 2>/dev/null && echo "Stopped." || echo "Not running."
