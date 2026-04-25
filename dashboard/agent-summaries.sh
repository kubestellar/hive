#!/bin/bash
# Fetch comprehensive agent exec summaries (task + progress + results)
# Called by dashboard to populate agent cards
#
# Priority: in-progress bead > fresh status file > stale status file
# A status file older than STALE_THRESHOLD_SEC is considered stale and
# loses to an in-progress bead (which reflects real-time work).

set +e
mkdir -p ~/.hive

STALE_THRESHOLD_SEC=1800  # 30 min

# Map agent name → beads directory
declare -A BEADS_DIR
BEADS_DIR[supervisor]="/home/dev/supervisor-beads"
BEADS_DIR[scanner]="/home/dev/scanner-beads"
BEADS_DIR[reviewer]="/home/dev/reviewer-beads"
BEADS_DIR[architect]="/home/dev/feature-beads"
BEADS_DIR[outreach]="/home/dev/outreach-beads"

# Read top in-progress bead title for an agent (returns empty if none)
bead_in_progress() {
  local dir="${BEADS_DIR[$1]:-}"
  [ -z "$dir" ] || [ ! -d "$dir" ] && return
  local line
  line=$(cd "$dir" && bd list 2>/dev/null | grep '^●' | head -1 || true)
  # Strip leading "● <id> ● P<N> " prefix to get the title
  echo "$line" | sed 's/^● [^ ]* ● P[0-9]* //' | sed 's/"/'\''/g'
}

# Check if status file is stale (older than STALE_THRESHOLD_SEC)
is_stale() {
  local file="$1"
  [ ! -f "$file" ] && return 0
  local now file_mtime age
  now=$(date +%s)
  file_mtime=$(stat -c %Y "$file" 2>/dev/null || stat -f %m "$file" 2>/dev/null || echo 0)
  age=$((now - file_mtime))
  [ "$age" -gt "$STALE_THRESHOLD_SEC" ]
}

{
  echo "{"
  echo '  "summaries": {'

  first=1
  for agent in supervisor scanner reviewer architect outreach; do
    file=~/.hive/${agent}_status.txt
    if [ -f "$file" ]; then
      task=$(grep '^TASK=' "$file" 2>/dev/null | cut -d= -f2- | sed 's/"/'\''/g')
      progress=$(grep '^PROGRESS=' "$file" 2>/dev/null | cut -d= -f2- | sed 's/"/'\''/g')
      results=$(grep '^RESULTS=' "$file" 2>/dev/null | cut -d= -f2- | sed 's/"/'\''/g')
      updated=$(grep '^UPDATED=' "$file" 2>/dev/null | cut -d= -f2-)
    else
      task=""
      progress=""
      results=""
      updated=""
    fi

    # Always check beads — prefer bead over stale status file
    bead=$(bead_in_progress "$agent")
    if [ -n "$bead" ]; then
      if [ -z "$task" ] || is_stale "$file"; then
        task="$bead"
        progress=""
        results=""
        updated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
      fi
    fi

    [ $first -eq 0 ] && echo ","
    first=0

    echo "    \"$agent\": {"
    echo "      \"task\": \"$task\","
    echo "      \"progress\": \"$progress\","
    echo "      \"results\": \"$results\","
    echo "      \"updated\": \"$updated\""
    echo -n "    }"
  done

  echo ""
  echo "  }"
  echo "}"
}
