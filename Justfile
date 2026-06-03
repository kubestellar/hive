# Justfile — KubeStellar Hive contributor commands
#
# Install just: brew install just (macOS) or cargo install just
# Usage: just contribute-hive [backend]

set shell := ["bash", "-euo", "pipefail", "-c"]

hive_image := env("HIVE_CONTRIBUTOR_IMAGE", "ghcr.io/kubestellar/hive-contributor:latest")
hive_hub := env("HIVE_HUB", "wss://hive.kubestellar.io:3001/contribute")
config_dir := env("HOME") + "/.config/hive"

# Show available commands
default:
    @just --list

# Register as a Hive contributor (one-time)
contribute-register:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{config_dir}}"
    echo "=== Hive Contributor Registration ==="
    echo ""
    read -rp "GitHub username: " github_user
    echo ""
    echo "Registering with hub..."
    HUB_HTTP=$(echo "{{hive_hub}}" | sed 's|^wss://|https://|;s|^ws://|http://|;s|/contribute$||')
    RESPONSE=$(curl -sf -X POST "${HUB_HTTP}/api/contribute/register" \
      -H "Content-Type: application/json" \
      -d "{\"github_username\": \"${github_user}\"}" 2>&1) || {
        echo "ERROR: Registration failed. Is the hub running at ${HUB_HTTP}?"
        exit 1
    }
    TOKEN=$(echo "$RESPONSE" | jq -r '.registration_token')
    CID=$(echo "$RESPONSE" | jq -r '.contributor_id')
    if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
      echo "ERROR: No token received. Response: $RESPONSE"
      exit 1
    fi
    cat > "{{config_dir}}/contributor.env" <<EOF
    HIVE_REGISTRATION_TOKEN=${TOKEN}
    HIVE_HUB={{hive_hub}}
    CONTRIBUTOR_ID=${CID}
    EOF
    echo ""
    echo "Registration successful!"
    echo "  Contributor ID: ${CID}"
    echo "  Config saved:   {{config_dir}}/contributor.env"
    echo ""
    echo ""
    echo "Next: run 'just contribute-login' to authenticate."

# Authenticate GitHub + CLI (run once before contribute-hive)
contribute-login backend="claude":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{config_dir}}"
    echo "=== Hive Contributor Login ==="
    echo ""

    # Step 1: GitHub authentication via gh CLI
    echo "── Step 1: GitHub Authentication ──"
    if command -v gh &>/dev/null; then
      if gh auth status &>/dev/null; then
        GH_USER=$(gh api user --jq '.login' 2>/dev/null || echo "")
        echo "Already authenticated as: ${GH_USER}"
      else
        echo "Logging into GitHub..."
        gh auth login --web --scopes "repo,read:org"
        GH_USER=$(gh api user --jq '.login' 2>/dev/null || echo "")
        echo "Authenticated as: ${GH_USER}"
      fi
      # Save the token for the contributor container
      GH_TOKEN=$(gh auth token 2>/dev/null || echo "")
      if [[ -n "$GH_TOKEN" ]]; then
        echo "GH_TOKEN=${GH_TOKEN}" > "{{config_dir}}/gh-auth.env"
        chmod 600 "{{config_dir}}/gh-auth.env"
        echo "GitHub token saved to {{config_dir}}/gh-auth.env"
      fi
    else
      echo "gh CLI not found. Install: https://cli.github.com"
      echo "Then run: gh auth login --web --scopes 'repo,read:org'"
      exit 1
    fi

    echo ""

    # Step 2: CLI authentication
    echo "── Step 2: {{backend}} CLI Authentication ──"
    case "{{backend}}" in
      claude)
        if command -v claude &>/dev/null; then
          echo "Launching Claude Code login..."
          claude login 2>/dev/null || echo "Claude login complete (or already authenticated)"
        else
          echo "Claude Code not installed. Install: npm i -g @anthropic-ai/claude-code"
          exit 1
        fi
        ;;
      copilot)
        if command -v copilot &>/dev/null; then
          echo "Copilot uses your gh auth. Already authenticated via Step 1."
        else
          echo "Copilot CLI not found. Install: gh extension install github/gh-copilot"
          exit 1
        fi
        ;;
      gemini)
        if command -v gemini &>/dev/null; then
          echo "Launching Gemini CLI login..."
          gemini auth login 2>/dev/null || echo "Gemini login complete (or already authenticated)"
        else
          echo "Gemini CLI not installed."
          exit 1
        fi
        ;;
      bob)
        if command -v bob &>/dev/null; then
          echo "Bob CLI detected. Authentication handled by bob on first run."
        else
          echo "Bob CLI not found."
          exit 1
        fi
        ;;
      goose)
        if command -v goose &>/dev/null; then
          echo "Goose CLI detected. Authentication handled by goose on first run."
        else
          echo "Goose CLI not found."
          exit 1
        fi
        ;;
      *)
        echo "Unknown backend: {{backend}}"
        echo "Supported: claude, copilot, gemini, bob, goose"
        exit 1
        ;;
    esac

    # Save chosen backend
    echo "AGENT_BACKEND={{backend}}" >> "{{config_dir}}/contributor.env"

    echo ""
    echo "Login complete!"
    echo "  GitHub:  authenticated"
    echo "  CLI:     {{backend}}"
    echo "  Config:  {{config_dir}}/"
    echo ""
    echo "Run 'just contribute-hive' to start contributing."

# Join the Hive swarm as a contributor
contribute-hive backend="claude":
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! -f "{{config_dir}}/contributor.env" ]]; then
      echo "Not registered yet. Run: just contribute-register"
      exit 1
    fi
    if [[ ! -f "{{config_dir}}/gh-auth.env" ]]; then
      echo "Not logged in yet. Run: just contribute-login {{backend}}"
      exit 1
    fi
    # Load contributor's personal GH token
    source "{{config_dir}}/gh-auth.env"
    echo "=== Hive Contributor Agent ==="
    echo "Backend:  {{backend}}"
    echo "Hub:      {{hive_hub}}"
    echo "GitHub:   $(gh api user --jq '.login' 2>/dev/null || echo 'authenticated')"
    echo ""
    echo "Your CLI tokens + GitHub identity stay local."
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
