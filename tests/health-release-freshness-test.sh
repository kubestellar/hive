#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
source "$ROOT_DIR/dashboard/health-lib.sh"

cutoff_day="2026-05-29T12:00:00Z"
cutoff_week="2026-05-23T12:00:00Z"

releases='[
  {
    "tag_name": "v0.3.24-nightly.20260503",
    "prerelease": true,
    "draft": false,
    "published_at": "2026-05-03T06:30:33Z"
  },
  {
    "tag_name": "v0.3.29-nightly.20260530",
    "prerelease": true,
    "draft": false,
    "published_at": "2026-05-30T06:30:33Z"
  },
  {
    "tag_name": "v0.3.28",
    "prerelease": false,
    "draft": false,
    "published_at": "2026-05-28T06:30:33Z"
  }
]'

result="$(release_freshness_from_json "$releases" "$cutoff_day" "$cutoff_week")"
if [[ "$result" != "1 1" ]]; then
  echo "expected fresh nightly and weekly releases, got: $result" >&2
  exit 1
fi

stale_releases='[
  {
    "tag_name": "v0.3.24-nightly.20260503",
    "prerelease": true,
    "draft": false,
    "published_at": "2026-05-03T06:30:33Z"
  },
  {
    "tag_name": "v0.3.20",
    "prerelease": false,
    "draft": false,
    "published_at": "2026-05-01T06:30:33Z"
  }
]'

result="$(release_freshness_from_json "$stale_releases" "$cutoff_day" "$cutoff_week")"
if [[ "$result" != "0 0" ]]; then
  echo "expected stale nightly and weekly releases, got: $result" >&2
  exit 1
fi
