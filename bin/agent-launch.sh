#!/bin/bash
# Invoked inside the tmux pane. Runs whatever AGENT_LAUNCH_CMD says to run, in
# AGENT_WORKDIR. Kept as a tiny wrapper so the tmux-new-session command itself
# stays simple and doesn't need complex quoting.
set -u

: "${AGENT_WORKDIR:?AGENT_WORKDIR is required (set in /etc/supervised-agent/agent.env)}"
: "${AGENT_LAUNCH_CMD:?AGENT_LAUNCH_CMD is required (set in /etc/supervised-agent/agent.env)}"

cd "$AGENT_WORKDIR"
exec /bin/bash -c "$AGENT_LAUNCH_CMD"
