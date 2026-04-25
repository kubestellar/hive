#!/bin/bash
# Fetch comprehensive agent exec summaries (task + progress + results)
# Called by dashboard to populate agent cards

set +e
mkdir -p ~/.hive

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

    # Enrich task with top in-progress bead when status file is empty or missing
    if [ -z "$task" ]; then
      bead=$(bead_in_progress "$agent")
      [ -n "$bead" ] && task="$bead (from beads)"
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
