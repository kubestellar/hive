#!/bin/bash
# token-collector.sh — Collect token usage from Claude Code JSONL session files.
#
# Scans ~/.claude/projects/*/  for active sessions (modified in last 24h),
# aggregates token counts per model and per session, writes JSON to
# /var/run/hive-metrics/tokens.json.
#
# Designed to run every 60s from the dashboard server or as a standalone cron.
# Performance: reads only recent files, streams with jq (no full parse).

set -euo pipefail

CLAUDE_PROJECTS="${HOME}/.claude/projects"
METRICS_DIR="/var/run/hive-metrics"
OUTPUT_FILE="${METRICS_DIR}/tokens.json"
LOOKBACK_HOURS="${TOKEN_LOOKBACK_HOURS:-24}"
LOOKBACK_SECS=$((LOOKBACK_HOURS * 3600))

mkdir -p "$METRICS_DIR"

# Find JSONL files modified in the lookback window
cutoff=$(date -d "-${LOOKBACK_SECS} seconds" +%s 2>/dev/null || date -v-${LOOKBACK_SECS}S +%s 2>/dev/null || echo 0)

BUDGET_RESET_DAY="${TOKEN_BUDGET_RESET_DAY:-4}"  # 0=Mon, 4=Fri (Claude resets Fri 7PM)

python3 - "$CLAUDE_PROJECTS" "$cutoff" "$LOOKBACK_HOURS" "$BUDGET_RESET_DAY" <<'PYEOF'
import json, os, sys, glob, time, datetime
from collections import defaultdict

projects_dir = sys.argv[1]
cutoff = int(sys.argv[2])
lookback_hours = int(sys.argv[3])
budget_reset_day = int(sys.argv[4])

now = time.time()
SECONDS_PER_HOUR = 3600
one_hour_ago = now - SECONDS_PER_HOUR

now_dt = datetime.datetime.now()
days_since_reset = (now_dt.weekday() - budget_reset_day) % 7
if days_since_reset == 0 and now_dt.hour == 0:
    days_since_reset = 7
weekly_cutoff = (now_dt - datetime.timedelta(days=days_since_reset)).replace(
    hour=0, minute=0, second=0, microsecond=0
).timestamp()

if not os.path.isdir(projects_dir):
    print(json.dumps({"error": "no claude projects dir", "sessions": [], "byModel": {}, "totals": {}}))
    sys.exit(0)

sessions = []
by_model = defaultdict(lambda: {"input": 0, "output": 0, "cacheRead": 0, "cacheCreate": 0, "messages": 0})
by_cli = defaultdict(lambda: {"input": 0, "output": 0, "cacheRead": 0, "cacheCreate": 0, "messages": 0, "sessions": 0})
by_agent = defaultdict(lambda: {"input": 0, "output": 0, "cacheRead": 0, "cacheCreate": 0, "messages": 0, "sessions": 0})
totals = {"input": 0, "output": 0, "cacheRead": 0, "cacheCreate": 0, "messages": 0, "sessions": 0}

weekly_by_agent = defaultdict(lambda: {"input": 0, "output": 0, "cacheRead": 0, "sessions": 0})
weekly_totals = {"input": 0, "output": 0, "cacheRead": 0, "sessions": 0}
hourly_by_agent = defaultdict(lambda: {"input": 0, "output": 0, "cacheRead": 0, "sessions": 0})

AGENT_KEYWORDS = {
    "scanner": ["scanner", "scanner-beads"],
    "reviewer": ["reviewer", "reviewer-beads"],
    "architect": ["architect", "architect-beads"],
    "outreach": ["outreach", "outreach-beads"],
    "supervisor": ["supervisor"],
}

for proj_name in os.listdir(projects_dir):
    proj_dir = os.path.join(projects_dir, proj_name)
    if not os.path.isdir(proj_dir):
        continue
    for fpath in glob.glob(os.path.join(proj_dir, "*.jsonl")):
        try:
            mtime = os.path.getmtime(fpath)
        except OSError:
            continue
        scan_cutoff = min(cutoff, weekly_cutoff)
        if mtime < scan_cutoff:
            continue

        sid = ""
        model = "unknown"
        agent = "unknown"
        inp = 0
        out = 0
        cache_read = 0
        cache_create = 0
        msg_count = 0
        first_ts = ""
        last_ts = ""
        agent_detected = False

        try:
            with open(fpath) as f:
                for line in f:
                    try:
                        d = json.loads(line)
                    except json.JSONDecodeError:
                        continue

                    if "sessionId" in d and not sid:
                        sid = d["sessionId"]

                    if not agent_detected and d.get("type") == "user":
                        raw = d.get("message", "")
                        if isinstance(raw, dict):
                            raw = raw.get("content", "")
                        if isinstance(raw, str):
                            rl = raw.lower()
                            for aname, kws in AGENT_KEYWORDS.items():
                                if any(kw in rl for kw in kws):
                                    agent = aname
                                    break
                        agent_detected = True

                    if d.get("type") == "assistant" and "message" in d:
                        msg = d["message"]
                        msg_count += 1

                        if msg.get("model") and msg["model"] != "<synthetic>":
                            model = msg["model"]

                        u = msg.get("usage", {})
                        inp += u.get("input_tokens", 0)
                        out += u.get("output_tokens", 0)
                        cache_read += u.get("cache_read_input_tokens", 0)
                        cc = u.get("cache_creation_input_tokens", 0)
                        if not cc:
                            cc_obj = u.get("cache_creation", {})
                            cc = cc_obj.get("ephemeral_1h_input_tokens", 0) + cc_obj.get("ephemeral_5m_input_tokens", 0)
                        cache_create += cc

                    ts = d.get("timestamp", "")
                    if ts:
                        if not first_ts:
                            first_ts = ts
                        last_ts = ts
        except (OSError, IOError):
            continue

        if msg_count == 0:
            continue

        # Detect CLI backend from model tag:
        # - "<synthetic>" = copilot (proxies to Claude but doesn't report real model)
        # - "claude-*" = claude code direct
        # - "gemini-*" = gemini cli
        # - "gpt-*" = copilot with GPT model
        # - other = unknown
        if model == "<synthetic>" or model == "unknown":
            cli = "copilot"
        elif model.startswith("claude"):
            cli = "claude"
        elif model.startswith("gemini"):
            cli = "gemini"
        elif model.startswith("gpt"):
            cli = "copilot"
        else:
            cli = "other"

        session_tokens = inp + out + cache_read

        # Weekly aggregation (for budget engine)
        if mtime >= weekly_cutoff and cli == "claude":
            weekly_by_agent[agent]["input"] += inp
            weekly_by_agent[agent]["output"] += out
            weekly_by_agent[agent]["cacheRead"] += cache_read
            weekly_by_agent[agent]["sessions"] += 1
            weekly_totals["input"] += inp
            weekly_totals["output"] += out
            weekly_totals["cacheRead"] += cache_read
            weekly_totals["sessions"] += 1

        # Hourly aggregation (for burn rate)
        if mtime >= one_hour_ago and cli == "claude":
            hourly_by_agent[agent]["input"] += inp
            hourly_by_agent[agent]["output"] += out
            hourly_by_agent[agent]["cacheRead"] += cache_read
            hourly_by_agent[agent]["sessions"] += 1

        # 24h aggregation (existing behavior)
        if mtime < cutoff:
            continue

        sessions.append({
            "id": sid[:12] if sid else os.path.basename(fpath)[:12],
            "model": model,
            "cli": cli,
            "agent": agent,
            "input": inp,
            "output": out,
            "cacheRead": cache_read,
            "cacheCreate": cache_create,
            "messages": msg_count,
            "total": session_tokens,
            "project": proj_name,
            "started": first_ts,
            "lastActive": last_ts,
            "mtime": int(mtime * 1000),
        })

        by_model[model]["input"] += inp
        by_model[model]["output"] += out
        by_model[model]["cacheRead"] += cache_read
        by_model[model]["cacheCreate"] += cache_create
        by_model[model]["messages"] += msg_count

        by_cli[cli]["input"] += inp
        by_cli[cli]["output"] += out
        by_cli[cli]["cacheRead"] += cache_read
        by_cli[cli]["cacheCreate"] += cache_create
        by_cli[cli]["messages"] += msg_count
        by_cli[cli]["sessions"] += 1

        by_agent[agent]["input"] += inp
        by_agent[agent]["output"] += out
        by_agent[agent]["cacheRead"] += cache_read
        by_agent[agent]["cacheCreate"] += cache_create
        by_agent[agent]["messages"] += msg_count
        by_agent[agent]["sessions"] += 1

        totals["input"] += inp
        totals["output"] += out
        totals["cacheRead"] += cache_read
        totals["cacheCreate"] += cache_create
        totals["messages"] += msg_count
        totals["sessions"] += 1

# Sort sessions by most recent first
sessions.sort(key=lambda s: s.get("mtime", 0), reverse=True)

for aname, astats in by_agent.items():
    s = astats["sessions"]
    total = astats["input"] + astats["output"] + astats["cacheRead"]
    astats["avgPerSession"] = total // s if s > 0 else 0

# Compute hourly burn rate per agent (all tokens and billable-only)
hourly_rates = {}
hourly_billable = {}
for aname, hstats in hourly_by_agent.items():
    hourly_rates[aname] = hstats["input"] + hstats["output"] + hstats["cacheRead"]
    hourly_billable[aname] = hstats["input"] + hstats["output"]

weekly_total_tokens = weekly_totals["input"] + weekly_totals["output"] + weekly_totals["cacheRead"]
weekly_billable_tokens = weekly_totals["input"] + weekly_totals["output"]

result = {
    "timestamp": int(time.time() * 1000),
    "lookbackHours": lookback_hours,
    "totals": totals,
    "byModel": dict(by_model),
    "byCli": dict(by_cli),
    "byAgent": dict(by_agent),
    "sessions": sessions[:20],
    "weekly": {
        "totals": weekly_totals,
        "totalTokens": weekly_total_tokens,
        "billableTokens": weekly_billable_tokens,
        "byAgent": dict(weekly_by_agent),
        "resetDay": budget_reset_day,
    },
    "hourlyBurnRate": {
        "total": sum(hourly_rates.values()),
        "billable": sum(hourly_billable.values()),
        "byAgent": hourly_rates,
        "byAgentBillable": hourly_billable,
    },
}

output = json.dumps(result)
print(output)
try:
    with open(os.environ.get('TOKEN_OUTPUT_FILE', '/var/run/hive-metrics/tokens.json'), 'w') as f:
        f.write(output)
except (OSError, IOError):
    pass
PYEOF
