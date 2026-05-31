#!/bin/bash

release_freshness_from_json() {
  local releases="$1"
  local one_day_ago="$2"
  local seven_days_ago="$3"

  echo "$releases" | jq -r --arg one_day_ago "$one_day_ago" --arg seven_days_ago "$seven_days_ago" '
    def release_time: (.published_at // "");
    def fresh_nightly:
      [
        .[]
        | select(.prerelease == true)
        | select(.draft == false)
        | select(.tag_name | test("nightly"))
      ]
      | sort_by(release_time)
      | reverse
      | .[0]?
      | if . == null then 0 elif release_time > $one_day_ago then 1 else 0 end;
    def fresh_weekly:
      [
        .[]
        | select(.prerelease == false)
        | select(.draft == false)
        | select((.tag_name | test("nightly")) | not)
      ]
      | sort_by(release_time)
      | reverse
      | .[0]?
      | if . == null then 0 elif release_time > $seven_days_ago then 1 else 0 end;
    "\(fresh_nightly) \(fresh_weekly)"
  '
}
