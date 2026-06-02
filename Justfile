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
    echo "Run 'just contribute-hive' to start contributing."

# Join the Hive swarm as a contributor
contribute-hive backend="claude":
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! -f "{{config_dir}}/contributor.env" ]]; then
      echo "Not registered yet. Run: just contribute-register"
      exit 1
    fi
    echo "=== Hive Contributor Agent ==="
    echo "Backend: {{backend}}"
    echo "Hub:     {{hive_hub}}"
    echo ""
    echo "Your CLI API tokens stay local. Hive provides GitHub access."
    echo ""
    docker run -it --rm \
      --name hive-contributor \
      -v "{{config_dir}}:/home/dev/.config/hive:ro" \
      -v "${HOME}/.claude:/home/dev/.claude:ro" \
      -v "${HOME}/.config/claude-code:/home/dev/.config/claude-code:ro" \
      -e HIVE_HUB="{{hive_hub}}" \
      -e AGENT_BACKEND="{{backend}}" \
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
