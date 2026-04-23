# macOS Support (launchd)

The supervised-agent runtime was built on Linux/systemd, but the same concepts map cleanly to macOS using **launchd** — Apple's equivalent of systemd.

This guide shows how to run a supervised agent on a Mac that stays on (Mac Mini, Mac Studio, always-on laptop, etc.).

---

## Concept mapping

| Linux (systemd) | macOS (launchd) | Role |
|---|---|---|
| `supervised-agent.service` | `LaunchAgent` plist | Keeps the supervisor alive, restarts on crash |
| `supervised-agent-healthcheck.timer` | Second `LaunchAgent` plist with `StartCalendarInterval` | Periodic healthcheck |
| `supervised-agent-renew.timer` | Third plist with 6-day interval | `/loop` cron renewal |
| `systemctl enable --now` | `launchctl load -w` | Start + enable at login |
| `systemctl stop` | `launchctl unload` | Stop |
| `journalctl -u supervised-agent` | `StandardOutPath` / `StandardErrorPath` in plist | Logs |
| `/etc/supervised-agent/agent.env` | Plist `EnvironmentVariables` dict or a sourced env file | Config |

---

## Quickstart

### 1. Install prerequisites

```sh
brew install tmux curl jq
# Install your AI CLI (e.g., Claude Code)
npm install -g @anthropic-ai/claude-code
claude /login
```

### 2. Create the config directory

```sh
mkdir -p ~/.config/supervised-agent
cp config/agent.env.example ~/.config/supervised-agent/agent.env
# Edit to match your setup:
nano ~/.config/supervised-agent/agent.env
```

### 3. Install the LaunchAgent

Copy the example plist, adjust paths, and load it:

```sh
# Copy the template
cp launchd/com.supervised-agent.plist.example ~/Library/LaunchAgents/com.supervised-agent.plist

# Edit: change all /Users/YOURUSER paths to your actual home directory
nano ~/Library/LaunchAgents/com.supervised-agent.plist

# Create log directory
mkdir -p ~/.local/state/supervised-agent

# Load (starts immediately + starts on every login)
launchctl load -w ~/Library/LaunchAgents/com.supervised-agent.plist
```

### 4. Verify it's running

```sh
# Check launchd status
launchctl list | grep supervised-agent

# Attach to the tmux session
tmux attach -t supervised-agent
# Detach: Ctrl+B then D
```

### 5. Uninstall

```sh
launchctl unload ~/Library/LaunchAgents/com.supervised-agent.plist
rm ~/Library/LaunchAgents/com.supervised-agent.plist
# Optionally remove config + state:
# rm -rf ~/.config/supervised-agent ~/.local/state/supervised-agent
```

---

## Healthcheck on macOS

On Linux, the healthcheck is a separate systemd timer. On macOS, use a second LaunchAgent with `StartCalendarInterval`:

```xml
<!-- ~/Library/LaunchAgents/com.supervised-agent.healthcheck.plist -->
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.supervised-agent.healthcheck</string>
    <key>ProgramArguments</key>
    <array>
        <string>/path/to/supervised-agent/bin/agent-healthcheck.sh</string>
    </array>
    <key>StartCalendarInterval</key>
    <array>
        <!-- Every 20 minutes: :00, :20, :40 -->
        <dict><key>Minute</key><integer>0</integer></dict>
        <dict><key>Minute</key><integer>20</integer></dict>
        <dict><key>Minute</key><integer>40</integer></dict>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>AGENT_LOG_FILE</key>
        <string>/Users/YOURUSER/.local/state/supervised-agent/heartbeat.log</string>
        <key>AGENT_SESSION_NAME</key>
        <string>supervised-agent</string>
        <key>AGENT_STALE_MAX_SEC</key>
        <string>1800</string>
        <key>AGENT_MAX_RESPAWNS</key>
        <string>3</string>
        <key>NTFY_TOPIC</key>
        <string>your-secret-topic</string>
    </dict>
    <key>StandardOutPath</key>
    <string>/Users/YOURUSER/.local/state/supervised-agent/healthcheck.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/YOURUSER/.local/state/supervised-agent/healthcheck.err</string>
</dict>
</plist>
```

---

## Renew timer on macOS

The renew timer kills and respawns the tmux session every 6 days to beat Claude Code's 7-day `/loop` expiry. On macOS, this is harder to express as a calendar interval (launchd doesn't have "every N days" natively).

**Recommended approach**: use a wrapper script that checks the session age:

```bash
#!/bin/bash
# renew-if-stale.sh — run hourly via launchd, only acts every 6 days
SESSION="${AGENT_SESSION_NAME:-supervised-agent}"
STATE_DIR="${HOME}/.local/state/supervised-agent"
STAMP="$STATE_DIR/last-renew"

# If stamp doesn't exist, create it and exit
[ -f "$STAMP" ] || { date +%s > "$STAMP"; exit 0; }

AGE=$(( $(date +%s) - $(cat "$STAMP") ))
if [ "$AGE" -ge 518400 ]; then  # 6 days in seconds
    tmux kill-session -t "$SESSION" 2>/dev/null
    date +%s > "$STAMP"
    # Supervisor will detect the missing session and respawn
fi
```

Then set a launchd plist with `StartInterval` of 3600 (hourly check).

---

## Differences from Linux

| Area | Linux | macOS |
|---|---|---|
| Shell | `/bin/bash` everywhere | `/bin/zsh` default; use `/opt/homebrew/bin/bash` for bash 5+ features (associative arrays) |
| `stat` flags | `stat -c %Y file` | `stat -f %m file` |
| Process management | `systemctl start/stop/restart` | `launchctl load/unload` |
| Auto-start | `systemctl enable` | `load -w` flag persists across reboots |
| Log viewing | `journalctl -u name -f` | `tail -f /path/to/log` (or Console.app) |
| File locking | `flock` (coreutils) | `flock` via `brew install util-linux` or use lockfile pattern |
| `date` command | GNU date (`date -d`) | BSD date (no `-d`; use `date -j -f`) |

---

## Alternative scheduler: standalone scanner script

On macOS, some deployments skip the full supervisor+tmux pattern entirely and use a **standalone scanner script** fired by launchd on a fixed schedule. The script does the scanning/state-tracking work in bash, then triggers the AI agent (via a Copilot CLI skill, tmux work order, or similar) only when there's actionable work.

This pattern is simpler when:
- The scanning logic is deterministic (no LLM needed for triage)
- You want the scanner to run even when the AI session is down
- You want to decouple scan cadence from agent availability

See [`examples/worker.sh.example`](../examples/worker.sh.example) for a reference implementation and [`examples/kubestellar-fixer.md`](../examples/kubestellar-fixer.md) for a full case study of this pattern in production.
