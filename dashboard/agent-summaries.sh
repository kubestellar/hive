#!/bin/bash
# Fetch comprehensive agent exec summaries (task + progress + results)
# Called by dashboard to populate agent cards

set +e
mkdir -p ~/.hive

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
