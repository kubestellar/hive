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

AGENT_PATTERNS = [
    ("supervisor", ["[agent:supervisor]", "supervisor-beads", "supervisor agent", "monitoring pass",
                    "you are the supervisor", "you are the supervisor agent"]),
    ("architect",  ["[agent:architect]", "architect-beads", "you are the architect",
                    "you are the kubestellar architect"]),
    ("reviewer",   ["[agent:reviewer]", "reviewer-beads", "you are the reviewer",
                    "you are the kubestellar reviewer"]),
    ("outreach",   ["[agent:outreach]", "outreach-beads", "you are the outreach",
                    "you are the kubestellar outreach"]),
    ("scanner",    ["[agent:scanner]", "scanner-beads", "you are the scanner",
                    "you are the kubestellar scanner"]),
]

PROJECT_DIR_AGENTS = {
    "gt-deacon-dogs-alpha": "dog-alpha",
    "gt-deacon-dogs-bravo": "dog-bravo",
    "gt-deacon-dogs-charlie": "dog-charlie",
    "gt-deacon-dogs-delta": "dog-delta",
    "gt-deacon-dogs-boot": "boot",
    "gt-deacon": "deacon",
    "gt-console-witness": "witness",
    "gt-mayor": "mayor",
}

def detect_agent_from_project(proj_name):
    for suffix, agent in PROJECT_DIR_AGENTS.items():
        if proj_name.endswith(suffix):
            return agent
    return None

def detect_agent_from_text(text):
    tl = text.lower()
    for aname, patterns in AGENT_PATTERNS:
        if any(p in tl for p in patterns):
            return aname
    return "unknown"

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
        proj_agent = detect_agent_from_project(proj_name)
        agent = proj_agent or "unknown"
        inp = 0
        out = 0
        cache_read = 0
        cache_create = 0
        msg_count = 0
        first_ts = ""
        last_ts = ""
        agent_detected = proj_agent is not None
        agent_scan_count = 0
        MAX_AGENT_SCAN = 5
        msg_times = []

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
                            detected = detect_agent_from_text(raw)
                            if detected != "unknown":
                                agent = detected
                                agent_detected = True
                        agent_scan_count += 1
                        if agent_scan_count >= MAX_AGENT_SCAN:
                            agent_detected = True

                    if d.get("type") == "assistant" and "message" in d:
                        msg = d["message"]
                        msg_count += 1
                        if ts:
                            msg_times.append(ts)

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
                        if d.get("type") == "user" and ts not in msg_times:
                            msg_times.append(ts)
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

        BUCKET_MINUTES = 10
        activity = []
        if msg_times and first_ts and last_ts:
            try:
                t0 = datetime.datetime.fromisoformat(first_ts.replace("Z", "+00:00")).timestamp()
                t1 = datetime.datetime.fromisoformat(last_ts.replace("Z", "+00:00")).timestamp()
                span = max(t1 - t0, 1)
                num_buckets = min(max(int(span / (BUCKET_MINUTES * 60)), 2), 30)
                bucket_size = span / num_buckets
                buckets = [0] * num_buckets
                for mt in msg_times:
                    try:
                        mts = datetime.datetime.fromisoformat(mt.replace("Z", "+00:00")).timestamp()
                        idx = min(int((mts - t0) / bucket_size), num_buckets - 1)
                        buckets[idx] += 1
                    except (ValueError, TypeError):
                        pass
                activity = buckets
            except (ValueError, TypeError):
                pass

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
            "activity": activity,
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

# ─── Copilot session scanning ─────────────────────────────────────────────
# Copilot CLI stores sessions in ~/.copilot/session-state/<uuid>/
# Events use a different schema but contain agent identity and model info.
# Token usage is available in session.shutdown events via modelMetrics.

copilot_dir = os.path.join(os.path.expanduser("~"), ".copilot", "session-state")
if os.path.isdir(copilot_dir):
    for session_id in os.listdir(copilot_dir):
        session_dir = os.path.join(copilot_dir, session_id)
        events_file = os.path.join(session_dir, "events.jsonl")
        if not os.path.isfile(events_file):
            continue
        try:
            mtime = os.path.getmtime(events_file)
        except OSError:
            continue
        scan_cutoff = min(cutoff, weekly_cutoff)
        if mtime < scan_cutoff:
            continue

        cp_model = "unknown"
        cp_agent = "unknown"
        cp_agent_detected = False
        cp_msg_count = 0
        cp_tool_count = 0
        cp_first_ts = ""
        cp_last_ts = ""
        cp_agent_scan_count = 0
        cp_inp = 0
        cp_out = 0
        cp_cache_read = 0
        cp_cache_create = 0
        cp_msg_times = []

        try:
            with open(events_file) as f:
                for line in f:
                    try:
                        d = json.loads(line)
                    except json.JSONDecodeError:
                        continue

                    etype = d.get("type", "")
                    data = d.get("data", {})
                    ts = d.get("timestamp", "")
                    if ts:
                        if not cp_first_ts:
                            cp_first_ts = ts
                        cp_last_ts = ts

                    if etype in ("user.message", "assistant.message") and ts:
                        cp_msg_times.append(ts)

                    if etype == "session.start":
                        cp_model = data.get("selectedModel", cp_model)

                    elif etype == "user.message" and not cp_agent_detected:
                        raw = data.get("content", "")
                        if isinstance(raw, str):
                            detected = detect_agent_from_text(raw)
                            if detected != "unknown":
                                cp_agent = detected
                                cp_agent_detected = True
                        cp_agent_scan_count += 1
                        if cp_agent_scan_count >= MAX_AGENT_SCAN:
                            cp_agent_detected = True

                    elif etype == "assistant.message":
                        cp_msg_count += 1

                    elif etype == "tool.execution_complete":
                        cp_tool_count += 1
                        m = data.get("model", "")
                        if m and m != "unknown":
                            cp_model = m

                    elif etype == "session.shutdown":
                        cm = data.get("currentModel", "")
                        if cm:
                            cp_model = cm
                        for mm_model, mm_stats in data.get("modelMetrics", {}).items():
                            u = mm_stats.get("usage", {})
                            cp_inp += u.get("inputTokens", 0)
                            cp_out += u.get("outputTokens", 0)
                            cp_cache_read += u.get("cacheReadTokens", 0)
                            cp_cache_create += u.get("cacheWriteTokens", 0)
        except (OSError, IOError):
            continue

        if cp_msg_count == 0:
            continue

        cli = "copilot"
        cp_total = cp_inp + cp_out + cp_cache_read

        BUCKET_MINUTES = 10
        cp_activity = []
        if cp_msg_times and cp_first_ts and cp_last_ts:
            try:
                t0 = datetime.datetime.fromisoformat(cp_first_ts.replace("Z", "+00:00")).timestamp()
                t1 = datetime.datetime.fromisoformat(cp_last_ts.replace("Z", "+00:00")).timestamp()
                span = max(t1 - t0, 1)
                num_buckets = min(max(int(span / (BUCKET_MINUTES * 60)), 2), 30)
                bucket_size = span / num_buckets
                buckets = [0] * num_buckets
                for mt in cp_msg_times:
                    try:
                        mts = datetime.datetime.fromisoformat(mt.replace("Z", "+00:00")).timestamp()
                        idx = min(int((mts - t0) / bucket_size), num_buckets - 1)
                        buckets[idx] += 1
                    except (ValueError, TypeError):
                        pass
                cp_activity = buckets
            except (ValueError, TypeError):
                pass

        sessions.append({
            "id": session_id[:12],
            "model": cp_model,
            "cli": cli,
            "agent": cp_agent,
            "input": cp_inp,
            "output": cp_out,
            "cacheRead": cp_cache_read,
            "cacheCreate": cp_cache_create,
            "messages": cp_msg_count,
            "toolCalls": cp_tool_count,
            "total": cp_total,
            "project": "copilot-session",
            "started": cp_first_ts,
            "lastActive": cp_last_ts,
            "mtime": int(mtime * 1000),
            "activity": cp_activity,
        })

        by_model[cp_model]["input"] += cp_inp
        by_model[cp_model]["output"] += cp_out
        by_model[cp_model]["cacheRead"] += cp_cache_read
        by_model[cp_model]["cacheCreate"] += cp_cache_create
        by_model[cp_model]["messages"] += cp_msg_count
        by_cli[cli]["input"] += cp_inp
        by_cli[cli]["output"] += cp_out
        by_cli[cli]["cacheRead"] += cp_cache_read
        by_cli[cli]["cacheCreate"] += cp_cache_create
        by_cli[cli]["messages"] += cp_msg_count
        by_cli[cli]["sessions"] += 1
        by_agent[cp_agent]["input"] += cp_inp
        by_agent[cp_agent]["output"] += cp_out
        by_agent[cp_agent]["cacheRead"] += cp_cache_read
        by_agent[cp_agent]["cacheCreate"] += cp_cache_create
        by_agent[cp_agent]["messages"] += cp_msg_count
        by_agent[cp_agent]["sessions"] += 1
        totals["input"] += cp_inp
        totals["output"] += cp_out
        totals["cacheRead"] += cp_cache_read
        totals["cacheCreate"] += cp_cache_create
        totals["messages"] += cp_msg_count
        totals["sessions"] += 1

        if mtime >= weekly_cutoff and cp_total > 0:
            weekly_by_agent[cp_agent]["input"] += cp_inp
            weekly_by_agent[cp_agent]["output"] += cp_out
            weekly_by_agent[cp_agent]["cacheRead"] += cp_cache_read
            weekly_by_agent[cp_agent]["sessions"] += 1
            weekly_totals["input"] += cp_inp
            weekly_totals["output"] += cp_out
            weekly_totals["cacheRead"] += cp_cache_read
            weekly_totals["sessions"] += 1

        if mtime >= one_hour_ago and cp_total > 0:
            hourly_by_agent[cp_agent]["input"] += cp_inp
            hourly_by_agent[cp_agent]["output"] += cp_out
            hourly_by_agent[cp_agent]["cacheRead"] += cp_cache_read
            hourly_by_agent[cp_agent]["sessions"] += 1

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
