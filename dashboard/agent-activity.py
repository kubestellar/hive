#!/usr/bin/env python3
"""Extract live activity summaries from Claude Code JSONL session files.

Tails active session files efficiently using byte offset tracking.
Produces human-readable 1-3 line summaries via template-based extraction.
Called by server.js every 5s, outputs JSON to stdout.
"""

import json, os, sys, glob, re, time, subprocess

CLAUDE_PROJECTS = os.path.expanduser("~/.claude/projects")
STATE_DIR = "/var/run/hive-metrics"
OFFSETS_FILE = os.path.join(STATE_DIR, "activity-offsets.json")
AGENT_MAP_FILE = os.path.join(STATE_DIR, "session-agent-map.json")
SUMMARY_CACHE_FILE = os.path.join(STATE_DIR, "activity-cache.json")

BOOTSTRAP_BYTES = 8192
STALE_SECONDS = 300

AGENT_TMUX_SESSION = {
    "supervisor": "supervisor",
    "scanner": "issue-scanner",
    "reviewer": "reviewer",
    "architect": "feature",
    "outreach": "outreach",
}

PANE_NOISE = re.compile(
    r"^[─━═].*[─━═]$|^❯|^\s*$|^ / commands|^ @ files|^  dev@|^  ⏵|"
    r"Claude (?:Opus|Sonnet|Haiku)|bypass permissions|shift\+tab|"
    r"Auto-update failed|ctrl\+q enqueue|\? help$|"
    r"~/kubestellar-console|~/hive|Read shell output|"
    r"Waiting up to \d+ seconds|"
    r"^[│└├┌┐┘┤┬┴┼╭╮╰╯]\s|^\s*[│└├]\s|"
    r"^\$ |^#\s|Scanner\s*─|Reviewer\s*─|Architect\s*─|"
    r"Outreach\s*─|Supervisor\s*─|"
    r"ctrl\+o to expand|^\d+ lines?\.\.\.|"
    r"^sleep \d|^cat |^head |^tail |^cd |^ls |^echo |"
    r"Status line is already configured|"
    r"Done \(\d+ tool|statusline-setup|⏎|⏷|⏵|"
    r"Configure statusline|No changes needed"
)

SPINNER_RE = re.compile(r"^[◐◑◒◓◉●◎○✻✶✸✹✢✽·*⏺] |Esc to cancel|"
                        r"Scampering|Evaporating|Perambulating|Puttering|"
                        r"Sautéed|Razzle-dazzling|Pondering|Cogitating")

AGENT_KEYWORDS = {
    "scanner": ["scanner", "scanner-beads"],
    "reviewer": ["reviewer", "reviewer-beads"],
    "architect": ["architect", "feature-beads"],
    "outreach": ["outreach", "outreach-beads"],
    "supervisor": ["supervisor"],
}

BASH_PATTERNS = [
    (re.compile(r"gh pr create"), "Creating pull request"),
    (re.compile(r"gh pr merge\b"), "Merging PR"),
    (re.compile(r"gh pr view (\d+)"), r"Viewing PR #\1"),
    (re.compile(r"gh pr checks (\d+)"), r"Checking CI on PR #\1"),
    (re.compile(r"gh issue view (\d+)"), r"Reading issue #\1"),
    (re.compile(r"gh issue list"), "Listing issues"),
    (re.compile(r"gh issue create"), "Creating issue"),
    (re.compile(r"gh issue edit (\d+)"), r"Editing issue #\1"),
    (re.compile(r"gh run list"), "Checking CI runs"),
    (re.compile(r"gh run view (\d+)"), r"Viewing CI run #\1"),
    (re.compile(r"gh api repos/([^/]+/[^/]+)/pulls"), r"Listing PRs on \1"),
    (re.compile(r"gh api repos/([^/]+/[^/]+)/issues"), r"Listing issues on \1"),
    (re.compile(r"gh api repos/([^/]+/[^/]+)"), r"GitHub API: \1"),
    (re.compile(r"git commit"), "Committing changes"),
    (re.compile(r"git push"), "Pushing to remote"),
    (re.compile(r"git checkout -b (\S+)"), r"Creating branch \1"),
    (re.compile(r"git diff"), "Reviewing diff"),
    (re.compile(r"npm run build"), "Running build"),
    (re.compile(r"npm run lint"), "Running linter"),
    (re.compile(r"npm test"), "Running tests"),
    (re.compile(r"npm install"), "Installing dependencies"),
    (re.compile(r"scp\b"), "Deploying files"),
    (re.compile(r'ssh\s+\S+\s+"([^"]{1,60})'), r"Remote: \1"),
    (re.compile(r"bd\s+(update|close|ready|list)\b"), r"Beads: \1"),
    (re.compile(r"curl\b.*api"), "Calling API"),
    (re.compile(r"cat\s+(\S+)"), lambda m: f"Reading {os.path.basename(m.group(1))}"),
    (re.compile(r"head\b|tail\b"), "Inspecting file"),
    (re.compile(r"ls\b|find\b"), "Listing files"),
    (re.compile(r"mkdir\b"), "Creating directory"),
    (re.compile(r"python3?\b"), "Running Python script"),
    (re.compile(r"node\b"), "Running Node script"),
]


def load_json_file(path):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError):
        return {}


def save_json_file(path, data):
    try:
        with open(path, "w") as f:
            json.dump(data, f)
    except OSError:
        pass


AGENT_NAME_MAP = {
    "scanner": "scanner",
    "reviewer": "reviewer",
    "architect": "architect",
    "outreach": "outreach",
    "supervisor": "supervisor",
}

KICK_PREFIX = "you are the kubestellar"


def detect_agent(filepath, agent_map):
    if filepath in agent_map:
        return agent_map[filepath]
    try:
        with open(filepath) as f:
            for line in f:
                try:
                    d = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if d.get("type") == "agent-name":
                    name = d.get("agentName", "").lower()
                    agent = AGENT_NAME_MAP.get(name, "")
                    if agent:
                        agent_map[filepath] = agent
                        return agent
                if d.get("type") == "user":
                    raw = d.get("message", "")
                    if isinstance(raw, dict):
                        raw = raw.get("content", "")
                    if isinstance(raw, list):
                        raw = " ".join(
                            str(c.get("text", "")) for c in raw if isinstance(c, dict)
                        )
                    if isinstance(raw, str):
                        rl = raw.lower()
                        if not rl.startswith(KICK_PREFIX) and "executor mode" not in rl:
                            agent_map[filepath] = ""
                            return ""
                        for aname, kws in AGENT_KEYWORDS.items():
                            if any(kw in rl for kw in kws):
                                agent_map[filepath] = aname
                                return aname
                    agent_map[filepath] = ""
                    return ""
    except OSError:
        pass
    return ""


def read_tail(filepath, offsets):
    stored = offsets.get(filepath, 0)
    try:
        size = os.path.getsize(filepath)
    except OSError:
        return [], offsets

    if size < stored:
        stored = max(0, size - BOOTSTRAP_BYTES)

    if stored == 0 and size > BOOTSTRAP_BYTES:
        stored = size - BOOTSTRAP_BYTES

    entries = []
    try:
        with open(filepath) as f:
            f.seek(stored)
            raw = f.read()
            new_offset = stored + len(raw.encode("utf-8", errors="replace"))
            for line in raw.splitlines():
                if not line.strip():
                    continue
                try:
                    d = json.loads(line)
                    if d.get("type") == "assistant":
                        entries.append(d)
                except json.JSONDecodeError:
                    continue
            offsets[filepath] = new_offset
    except OSError:
        pass
    return entries, offsets


def describe_bash(cmd):
    cmd = cmd.strip()
    if not cmd:
        return None
    for pattern, template in BASH_PATTERNS:
        m = pattern.search(cmd)
        if m:
            if callable(template):
                return template(m)
            return m.expand(template) if "\\" in template else template
    parts = re.split(r"\s*&&\s*|\s*\|\s*", cmd)
    last = parts[-1].strip()
    words = last.split()
    if words:
        prog = os.path.basename(words[0])
        if len(words) > 1:
            return f"{prog} {' '.join(words[1:])}"[:80]
        return prog[:80]
    return cmd[:80]


def describe_tool(content):
    name = content.get("name", "")
    inp = content.get("input", {})

    if name == "Bash":
        return describe_bash(inp.get("command", ""))
    elif name in ("Edit", "Write"):
        fp = inp.get("file_path", "")
        return f"Editing {os.path.basename(fp)}" if fp else None
    elif name == "Read":
        fp = inp.get("file_path", "")
        return f"Reading {os.path.basename(fp)}" if fp else None
    elif name == "Agent":
        desc = inp.get("description", "")[:60]
        return f"Sub-agent: {desc}" if desc else "Dispatching sub-agent"
    elif name in ("WebSearch", "WebFetch"):
        q = inp.get("query", inp.get("url", ""))[:60]
        return f"Web search: {q}" if q else "Searching web"
    elif name == "Skill":
        sk = inp.get("skill", "")
        return f"Running /{sk}" if sk else None
    return None


def first_sentence(text):
    text = text.strip()
    if not text:
        return None
    for sep in [". ", ".\n", "\n\n"]:
        idx = text.find(sep)
        if idx > 0:
            text = text[: idx + 1]
            break
    text = text[:120]
    if text.startswith("I'll ") or text.startswith("Let me "):
        return text
    if len(text) > 20:
        return text
    return None


def summarize_entries(entries):
    tools = []
    last_text = None

    for entry in reversed(entries):
        msg = entry.get("message", {})
        for content in msg.get("content", []):
            ct = content.get("type")
            if ct == "tool_use" and len(tools) < 3:
                desc = describe_tool(content)
                if desc and desc not in tools:
                    tools.append(desc)
            elif ct == "text" and last_text is None:
                last_text = first_sentence(content.get("text", ""))

    lines = []
    if tools:
        lines.append(tools[0])
    if last_text and last_text not in lines:
        lines.append(last_text)
    for t in tools[1:]:
        if len(lines) >= 5:
            break
        if t not in lines:
            lines.append(t)

    return "\n".join(lines[:5])


def scrape_tmux(session):
    """Extract meaningful output lines from a tmux pane."""
    try:
        raw = subprocess.check_output(
            ["tmux", "capture-pane", "-t", session, "-p", "-S", "-50"],
            timeout=5, text=True, stderr=subprocess.DEVNULL
        )
    except (subprocess.SubprocessError, OSError):
        return None

    lines = []
    is_working = False
    for line in raw.splitlines():
        if PANE_NOISE.search(line):
            continue
        if SPINNER_RE.search(line):
            is_working = True
            cleaned = re.sub(r"^[◐◑◒◓◉●◎○✻✶✸✹✢✽·*⏺] ", "", line)
            cleaned = re.sub(r"\s*\(Esc to cancel.*", "", cleaned)
            cleaned = re.sub(r"\s*\(\d+[ms] .*$", "", cleaned)
            cleaned = cleaned.strip()
            if cleaned and len(cleaned) > 5:
                lines.append(cleaned)
        elif line.strip() and len(line.strip()) > 5:
            lines.append(line.strip())

    unique = []
    for l in lines:
        if l not in unique:
            unique.append(l)
    result = unique[-3:] if unique else []
    return ("\n".join(result), is_working) if result else None


def main():
    os.makedirs(STATE_DIR, exist_ok=True)
    offsets = load_json_file(OFFSETS_FILE)
    agent_map = load_json_file(AGENT_MAP_FILE)
    cached = load_json_file(SUMMARY_CACHE_FILE)

    now = time.time()
    best_per_agent = {}

    if not os.path.isdir(CLAUDE_PROJECTS):
        print(json.dumps(cached))
        return

    for proj_name in os.listdir(CLAUDE_PROJECTS):
        proj_dir = os.path.join(CLAUDE_PROJECTS, proj_name)
        if not os.path.isdir(proj_dir):
            continue
        for fpath in glob.glob(os.path.join(proj_dir, "*.jsonl")):
            try:
                mtime = os.path.getmtime(fpath)
            except OSError:
                continue
            if now - mtime > STALE_SECONDS:
                continue
            agent = detect_agent(fpath, agent_map)
            if not agent:
                continue
            prev = best_per_agent.get(agent)
            if prev is None or mtime > prev[1]:
                best_per_agent[agent] = (fpath, mtime)

    for agent, (fpath, mtime) in best_per_agent.items():
        entries, offsets = read_tail(fpath, offsets)
        summary = summarize_entries(entries)
        active = (now - mtime) < STALE_SECONDS
        if summary:
            cached[agent] = {
                "summary": summary,
                "ts": int(mtime * 1000),
                "active": active,
            }
        elif agent in cached:
            cached[agent]["active"] = active

    for agent in list(cached.keys()):
        if agent not in best_per_agent:
            cached[agent]["active"] = False

    for agent, session in AGENT_TMUX_SESSION.items():
        if agent in best_per_agent and cached.get(agent, {}).get("active"):
            continue
        result = scrape_tmux(session)
        if result:
            summary, is_working = result
            cached[agent] = {
                "summary": summary,
                "ts": int(now * 1000),
                "active": is_working,
            }

    save_json_file(OFFSETS_FILE, offsets)
    save_json_file(AGENT_MAP_FILE, agent_map)
    save_json_file(SUMMARY_CACHE_FILE, cached)
    print(json.dumps(cached))


if __name__ == "__main__":
    main()
