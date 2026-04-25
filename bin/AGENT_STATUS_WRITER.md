# Agent Status Writer — Live Executive Summaries

Agents should write comprehensive status updates **while working** so the dashboard shows real-time progress.

## Quick Start

```bash
# At start of work phase
agent-status-writer <agent_name> "Scanning X for Y" "" "starting..."

# Mid-task (progress update)
agent-status-writer <agent_name> "Scanning X for Y" "2/5 repos done (console, docs)" "Found 12 issues so far"

# Final state
agent-status-writer <agent_name> "Scanning X for Y" "5/5 repos complete" "Found 24 issues total: 16 unassigned, 8 hold"
```

## Parameters

| Param | Example | Notes |
|-------|---------|-------|
| `agent_name` | `scanner` | Must match agent tmux session |
| `task` | `Scanning kubestellar/console for open issues` | What you're doing |
| `progress` | `3/5 repos (console, docs, console-kb)` | Current step/percent |
| `results` | `Found 12 issues: 8 unassigned, 4 waiting-on-feedback` | What you've found/done |

## File Format

Status written to: `~/.hive/<agent>_status.txt`

```
AGENT=scanner
TASK=Scanning kubestellar/console for open issues
PROGRESS=3/5 repos complete
RESULTS=Found 12 issues
UPDATED=2026-04-25T02:47:00+00:00
```

## Dashboard Integration

- Endpoint: `/api/summaries`
- Updates every refresh (5s default)
- Shows task + progress + results on each agent card

## Usage Patterns

### Scanner Pass
```bash
# Start
agent-status-writer scanner "Scanning 5 repos for open issues" "" ""

# Each repo
agent-status-writer scanner "Scanning 5 repos for open issues" "1/5: scanning kubestellar/console" "0 issues found so far"
agent-status-writer scanner "Scanning 5 repos for open issues" "2/5: scanning console-kb" "8 issues found so far"

# Final
agent-status-writer scanner "Scanning 5 repos for open issues" "5/5 complete" "Found 24 issues: 16 unassigned, 8 hold"
```

### Reviewer Pass
```bash
agent-status-writer reviewer "Checking CI health and release freshness" "Step 1/4: Coverage check" ""
agent-status-writer reviewer "Checking CI health and release freshness" "Step 2/4: OAuth code check" "✓ Coverage 94%"
agent-status-writer reviewer "Checking CI health and release freshness" "Step 3/4: Brew formula check" "✓ Coverage 94%, ✓ OAuth present"
agent-status-writer reviewer "Checking CI health and release freshness" "Complete" "✓ Coverage 94%, ✓ OAuth, Brew v1.3.0 needed"
```

### Architect PR Work
```bash
agent-status-writer architect "Fixing issue #1234: useState batching bug" "Step 1/3: Understanding issue" ""
agent-status-writer architect "Fixing issue #1234: useState batching bug" "Step 2/3: Writing fix + tests" "PR drafted, testing locally"
agent-status-writer architect "Fixing issue #1234: useState batching bug" "Complete" "PR #9992 created and CI passing"
```

## Tips

- Update **frequently** (every 10-30s during active work)
- Be specific: "Checking brew" not just "working"
- Include counts/numbers: "3/5", "24 issues", "CI: 94%"
- Show what's **wrong**: "Brew v1.2.0 → needs 1.3.0"
- Include ✓/✗ symbols for quick scanning

