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
      echo "ERROR: ${MSG:-No token received}"
      exit 1
    fi
    cat > "{{config_dir}}/contributor.env" <<EOF
    HIVE_REGISTRATION_TOKEN=${TOKEN}
    HIVE_HUB={{hive_hub}}
    CONTRIBUTOR_ID=${CID}
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

    echo ""
    echo "✓ Setup complete!"
    echo "  GitHub:  ${GH_USER}"
    echo "  CLI:     {{backend}}"
    echo "  Hub:     {{hive_hub}}"
    echo ""
    echo "Run 'just contribute-hive' to start contributing."

# Start contributing — launches the agent container
contribute-hive backend="claude":
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! -f "{{config_dir}}/contributor.env" ]]; then
      echo "Not set up yet. Run: just contribute-setup {{backend}}"
      exit 1
    fi
    if [[ ! -f "{{config_dir}}/gh-auth.env" ]]; then
      echo "Not set up yet. Run: just contribute-setup {{backend}}"
      exit 1
    fi
    source "{{config_dir}}/gh-auth.env"
    echo "=== Hive Contributor Agent ==="
    echo "Backend:  {{backend}}"
    echo "Hub:      {{hive_hub}}"
    echo "GitHub:   $(gh api user --jq '.login' 2>/dev/null || echo 'authenticated')"
    echo ""
    docker run -it --rm \
      --name hive-contributor \
      -v "{{config_dir}}:/home/dev/.config/hive:ro" \
      -v "${HOME}/.claude:/home/dev/.claude:ro" \
      -v "${HOME}/.config/claude-code:/home/dev/.config/claude-code:ro" \
      -v "${HOME}/.config/gh:/home/dev/.config/gh:ro" \
      -e HIVE_HUB="{{hive_hub}}" \
      -e AGENT_BACKEND="{{backend}}" \
      -e GH_TOKEN="${GH_TOKEN}" \
      -e HIVE_USE_CONTRIBUTOR_GH=true \
      {{hive_image}}

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

# Stop contributing (if running in background)
contribute-stop:
    @docker stop hive-contributor 2>/dev/null && echo "Stopped." || echo "Not running."
