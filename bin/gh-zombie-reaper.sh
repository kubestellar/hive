#!/bin/bash
# gh-zombie-reaper.sh — Kill gh API processes older than MAX_AGE_SECONDS
# Prevents rate-limit retry storms from accumulating zombie processes.
#
# Runs via cron every 2 minutes. Kills any gh process that has been
# running longer than the threshold (default 120s). Legitimate gh calls
# complete in <10s; anything older is stuck in a rate-limit retry loop.

MAX_AGE_SECONDS="${GH_ZOMBIE_MAX_AGE:-120}"
LOG="/var/log/gh-zombie-reaper.log"
KILLED=0

# Use etimes (elapsed seconds) for accurate age calculation
while IFS= read -r line; do
  pid=$(echo "$line" | awk '{print $1}')
  age_seconds=$(echo "$line" | awk '{print $2}')

  if [ "$age_seconds" -gt "$MAX_AGE_SECONDS" ] 2>/dev/null; then
    cmdline=$(ps -p "$pid" -o args= 2>/dev/null)
    kill "$pid" 2>/dev/null
    echo "$(date -Iseconds) KILLED pid=$pid age=${age_seconds}s cmd=$cmdline" >> "$LOG"
    ((KILLED++))
  fi
done < <(ps -eo pid,etimes,comm | awk '$3 == "gh" {print $1, $2}')

if [ "$KILLED" -gt 0 ]; then
  echo "$(date -Iseconds) Reaped $KILLED zombie gh processes" >> "$LOG"
fi
