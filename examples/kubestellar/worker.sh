#!/bin/bash
# KubeStellar Fix Loop — Worker (scanner + notifier)
# Managed by launchd. Runs one cycle per invocation.
# Scans all 6 repos, updates SQLite state, sends ntfy.
# The Copilot skill reads the DB and does actual fixes.
set -euo pipefail

# Use hive GitHub App token — never fall back to personal gh auth
unset GITHUB_TOKEN 2>/dev/null || true
GH_APP_TOKEN_CACHE="/var/run/hive-metrics/gh-app-token.cache"
if [[ -f "$GH_APP_TOKEN_CACHE" ]]; then
  export GH_TOKEN="$(cat "$GH_APP_TOKEN_CACHE")"
elif [[ -n "${HIVE_GITHUB_TOKEN:-}" ]]; then
  export GH_TOKEN="$HIVE_GITHUB_TOKEN"
else
  echo "ERROR: no hive app token available" >&2
  exit 1
fi

DIR="$HOME/.kubestellar-fix-loop"
DB="$DIR/state.db"
LOCKFILE="$DIR/cycle.lock"
LOGFILE="$DIR/worker.log"
NTFY_TOPIC="ks-fix-loop"
REPOS="console console-kb console-marketplace docs kubestellar-mcp claude-plugins"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$LOGFILE"; }

ntfy() {
  local title="$1" body="$2" prio="${3:-default}" tags="${4:-robot}"
  curl -sf -o /dev/null -m 10 \
    -H "Title: $title" -H "Priority: $prio" -H "Tags: $tags" \
    -d "$body" "${NTFY_SERVER:-https://ntfy.sh}/$NTFY_TOPIC" 2>/dev/null || log "WARN: ntfy send failed"
}

# ── Lock ─────────────────────────────────────────────────────────────────────
if [ -f "$LOCKFILE" ]; then
  LOCK_AGE=$(( $(date +%s) - $(stat -f %m "$LOCKFILE" 2>/dev/null || echo 0) ))
  if [ "$LOCK_AGE" -lt 600 ]; then
    log "Cycle locked (age ${LOCK_AGE}s < 600s). Skipping."
    exit 0
  fi
  log "Stale lock (age ${LOCK_AGE}s). Removing."
  rm -f "$LOCKFILE"
fi
echo $$ > "$LOCKFILE"
trap 'rm -f "$LOCKFILE"' EXIT

# ── Init SQLite ──────────────────────────────────────────────────────────────
init_db() {
  sqlite3 "$DB" <<'SQL'
CREATE TABLE IF NOT EXISTS items (
  repo TEXT NOT NULL,
  type TEXT NOT NULL,           -- 'issue' or 'pr'
  number INTEGER NOT NULL,
  title TEXT,
  author TEXT,
  created_at TEXT,
  status TEXT DEFAULT 'open',   -- open, triaged, fixing, fixed, closed, skip
  last_seen TEXT,
  fix_attempts INTEGER DEFAULT 0,
  fix_pr TEXT,
  notes TEXT,
  PRIMARY KEY (repo, type, number)
);
CREATE TABLE IF NOT EXISTS cycles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  total_issues INTEGER,
  total_prs INTEGER,
  items_fixed INTEGER DEFAULT 0,
  items_closed INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS repo_counts (
  repo TEXT NOT NULL,
  cycle_id INTEGER NOT NULL,
  issues INTEGER,
  prs INTEGER,
  PRIMARY KEY (repo, cycle_id)
);
SQL
}

init_db

# ── Start cycle ──────────────────────────────────────────────────────────────
CYCLE_ID=$(sqlite3 "$DB" "INSERT INTO cycles (started_at) VALUES ('$(date -u +%Y-%m-%dT%H:%M:%SZ)'); SELECT last_insert_rowid();")
log "=== Cycle $CYCLE_ID started ==="

# ── Scan repos ───────────────────────────────────────────────────────────────
TOTAL_I=0
TOTAL_P=0
SUMMARY=""
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

for repo in $REPOS; do
  # Fetch issues
  ISSUES=$(gh issue list --repo "kubestellar/$repo" --state open --limit 200 \
    --json number,title,author,createdAt 2>/dev/null || echo "[]")
  IC=$(echo "$ISSUES" | jq 'length' 2>/dev/null || echo 0)

  # Fetch PRs
  PRS=$(gh pr list --repo "kubestellar/$repo" --state open --limit 100 \
    --json number,title,author,createdAt 2>/dev/null || echo "[]")
  PC=$(echo "$PRS" | jq 'length' 2>/dev/null || echo 0)

  TOTAL_I=$((TOTAL_I + IC))
  TOTAL_P=$((TOTAL_P + PC))
  SUMMARY+="$repo: ${IC}i/${PC}pr  "

  # Save counts
  sqlite3 "$DB" "INSERT OR REPLACE INTO repo_counts (repo, cycle_id, issues, prs) VALUES ('$repo', $CYCLE_ID, $IC, $PC);"

  # Upsert items — mark seen, detect new
  echo "$ISSUES" | jq -c '.[]' 2>/dev/null | while IFS= read -r item; do
    NUM=$(echo "$item" | jq -r '.number')
    TITLE=$(echo "$item" | jq -r '.title' | sed "s/'/''/g")
    AUTHOR=$(echo "$item" | jq -r '.author.login // .author')
    CREATED=$(echo "$item" | jq -r '.createdAt')
    EXISTS=$(sqlite3 "$DB" "SELECT status FROM items WHERE repo='$repo' AND type='issue' AND number=$NUM;" 2>/dev/null)
    if [ -z "$EXISTS" ]; then
      sqlite3 "$DB" "INSERT INTO items (repo, type, number, title, author, created_at, last_seen) VALUES ('$repo','issue',$NUM,'$TITLE','$AUTHOR','$CREATED','$NOW');"
      log "NEW issue: $repo#$NUM — $TITLE"
    else
      sqlite3 "$DB" "UPDATE items SET last_seen='$NOW', title='$TITLE' WHERE repo='$repo' AND type='issue' AND number=$NUM;"
    fi
  done

  echo "$PRS" | jq -c '.[]' 2>/dev/null | while IFS= read -r item; do
    NUM=$(echo "$item" | jq -r '.number')
    TITLE=$(echo "$item" | jq -r '.title' | sed "s/'/''/g")
    AUTHOR=$(echo "$item" | jq -r '.author.login // .author')
    CREATED=$(echo "$item" | jq -r '.createdAt')
    EXISTS=$(sqlite3 "$DB" "SELECT status FROM items WHERE repo='$repo' AND type='pr' AND number=$NUM;" 2>/dev/null)
    if [ -z "$EXISTS" ]; then
      sqlite3 "$DB" "INSERT INTO items (repo, type, number, title, author, created_at, last_seen) VALUES ('$repo','pr',$NUM,'$TITLE','$AUTHOR','$CREATED','$NOW');"
      log "NEW pr: $repo#$NUM — $TITLE"
    else
      sqlite3 "$DB" "UPDATE items SET last_seen='$NOW', title='$TITLE' WHERE repo='$repo' AND type='pr' AND number=$NUM;"
    fi
  done

  # Detect closed items (in DB as open but not in current scan)
  sqlite3 "$DB" "SELECT number, type, title FROM items WHERE repo='$repo' AND status IN ('open','triaged','fixing') AND last_seen < '$NOW';" 2>/dev/null | while IFS='|' read -r num typ title; do
    [ -z "$num" ] && continue
    sqlite3 "$DB" "UPDATE items SET status='closed' WHERE repo='$repo' AND type='$typ' AND number=$num;"
    log "CLOSED $typ: $repo#$num — $title"
    # ntfy for closure
    ntfy "✅ ${repo}#${num} closed" "${title}" "default" "white_check_mark"
  done
done

# ── Cycle complete ───────────────────────────────────────────────────────────
CLOSED_THIS=$(sqlite3 "$DB" "SELECT COUNT(*) FROM items WHERE status='closed' AND last_seen < '$NOW';" 2>/dev/null || echo 0)
sqlite3 "$DB" "UPDATE cycles SET completed_at='$(date -u +%Y-%m-%dT%H:%M:%SZ)', total_issues=$TOTAL_I, total_prs=$TOTAL_P, items_closed=$CLOSED_THIS WHERE id=$CYCLE_ID;"

# ── ntfy: cycle summary ─────────────────────────────────────────────────────
ACTIONABLE=$(sqlite3 "$DB" "SELECT COUNT(*) FROM items WHERE status='open';" 2>/dev/null || echo "?")
ntfy "🔄 Scan #$CYCLE_ID — ${TOTAL_I}i/${TOTAL_P}p" "$SUMMARY
Actionable: $ACTIONABLE  Closed this cycle: $CLOSED_THIS" "default" "repeat"

log "Cycle $CYCLE_ID complete: ${TOTAL_I}i/${TOTAL_P}p, closed=$CLOSED_THIS, actionable=$ACTIONABLE"

# ── Write trigger for Copilot skill ─────────────────────────────────────────
echo "$CYCLE_ID" > "$DIR/trigger"
