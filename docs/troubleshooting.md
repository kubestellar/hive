# Troubleshooting

## The `/loop` prompt never fires — the agent just sits at the prompt

Cause: the supervisor's `AGENT_READY_MARKER` doesn't appear in the agent's TUI, so the supervisor gives up before sending `AGENT_LOOP_PROMPT`.

Fix: attach to the session, look at the footer the agent shows when it's accepting input (for Claude Code v2.x it's "bypass permissions on"), and put that string in `AGENT_READY_MARKER` in `/etc/supervised-agent/agent.env`. Then:

```sh
sudo systemctl stop supervised-agent
sudo -u "$AGENT_USER" tmux kill-session -t "$AGENT_SESSION_NAME"
sudo systemctl start supervised-agent
```

## A permission prompt blocks every `/loop` iteration

Cause: the agent is hitting an interactive prompt that needs a human to dismiss. For Claude Code writing to `memory/` triggers a "sensitive file" prompt even under `--dangerously-skip-permissions`.

Fix: find the unique text of the "accept" option (e.g. `Yes, and always allow access to`) and put it in `AGENT_AUTO_APPROVE_PHRASE`. The supervisor will auto-send `Down Enter` the next time that phrase appears in the pane.

If your agent needs multiple different approvals, extend `approve_prompt_if_present()` in `bin/agent-supervisor.sh` to loop over a list of phrases.

## `systemctl restart supervised-agent` didn't pick up my new `AGENT_LOOP_PROMPT`

This is a footgun and it's worth repeating: **plain `systemctl restart` doesn't cause a session respawn.** tmux sessions survive supervisor exit, so when the new supervisor starts and sees a "healthy" old session with an "alive" agent, it does nothing. The old session is still running the old prompt.

For a full reset you need:

```sh
sudo systemctl stop supervised-agent
sudo -u "$AGENT_USER" tmux kill-session -t "$AGENT_SESSION_NAME"
sudo systemctl start supervised-agent
```

Now the new supervisor finds no session, creates one, and sends the current `AGENT_LOOP_PROMPT`.

## ntfy push never arrives

Run in order:

```sh
# 1. Manual test — does ntfy work at all?
curl -d "test" "$NTFY_SERVER/$NTFY_TOPIC"

# 2. Is the healthcheck env correct?
systemctl cat supervised-agent-healthcheck.service
# Look for EnvironmentFile=/etc/supervised-agent/agent.env

# 3. Run the healthcheck by hand to see any errors:
sudo -u "$AGENT_USER" env $(cat /etc/supervised-agent/agent.env | xargs) /usr/local/bin/agent-healthcheck.sh

# 4. Is the log actually stale?
stat "$AGENT_LOG_FILE"
```

If step 1 works but the healthcheck doesn't push, the most common cause is `NTFY_TOPIC` being blank or misspelled in the env file.

## Supervisor keeps respawning even though the agent looks healthy

Check the `agent_alive()` heuristic in `bin/agent-supervisor.sh`. It considers the agent dead if no non-shell process is running under the pane PID. If your launch command has an extra shell wrapper, the supervisor may mistake the wrapper for "dead." Remove the wrapper, or edit the heuristic.

## Agent writes the heartbeat but scans are obviously broken

The healthcheck only knows about file mtime. If the agent's iteration code is silently noop-ing but still remembers to append to the log, the healthcheck is happy. Two defensive patterns:

1. **Put counts in the log.** If `Issues triaged: 0` for 8 firings in a row, something's wrong. You'll see it in `tail -f $AGENT_LOG_FILE`.
2. **Add your own smoke-check** as a second systemd timer that hits an external endpoint (GitHub API, your app) and cross-checks the agent's reported state.

## Where do I see what the agent is doing right now?

```sh
sudo -u "$AGENT_USER" tmux attach -t "$AGENT_SESSION_NAME"
# Ctrl+B, D to detach — session keeps running.

# Or just peek without attaching:
sudo -u "$AGENT_USER" tmux capture-pane -t "$AGENT_SESSION_NAME" -p -S -50
```

## Clean reinstall

```sh
sudo ./uninstall.sh
# (optionally remove /etc/supervised-agent/agent.env and the heartbeat log dir)
sudo ./install.sh
```
