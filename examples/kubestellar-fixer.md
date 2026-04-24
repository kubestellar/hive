# Case study: KubeStellar autonomous fixer

This documents a production deployment of the hive pattern on the [KubeStellar](https://kubestellar.io) project — a CNCF Sandbox multi-cluster management platform. The system autonomously triages, fixes, and merges across 6 GitHub repositories with a target SLA of <30 minutes from issue filed to PR merged.

> **This is a case study, not a tutorial.** It shows how the generic patterns from this repo were composed into a specific deployment. Your setup will differ — use this for inspiration, not as a recipe to copy verbatim.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Mac Mini (always-on)                     │
│                                                                 │
│   ┌──────────────┐     ┌──────────────┐     ┌───────────────┐  │
│   │   launchd     │────▶│  worker.sh   │────▶│  state.db     │  │
│   │  (every 15m)  │     │  (scanner)   │     │  (SQLite)     │  │
│   └──────────────┘     └──────┬───────┘     └───────┬───────┘  │
│                               │                     │           │
│                          ntfy push              trigger file    │
│                               │                     │           │
│                               ▼                     ▼           │
│                        ┌────────────┐     ┌─────────────────┐  │
│                        │  phone     │     │  Copilot CLI     │  │
│                        │  (ntfy)    │     │  skills          │  │
│                        └────────────┘     │  (fix-loop.md)   │  │
│                                           │  (auto-qa.md)    │  │
│                                           │  (hygiene.md)    │  │
│                                           └────────┬────────┘  │
│                                                    │            │
└────────────────────────────────────────────────────┼────────────┘
                                                     │
                                              git push, gh pr
                                                     │
                                                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                           GitHub                                │
│                                                                 │
│   ┌───────────────────────────────────────────────────────┐     │
│   │  workflow-failure-issue.yml                            │     │
│   │  (auto-files issues when any workflow fails)           │     │
│   └──────────────────────────┬────────────────────────────┘     │
│                              │                                   │
│                              ▼                                   │
│   ┌───────────────────────────────────────────────────────┐     │
│   │  ai-fix.yml                                           │     │
│   │  (auto-dispatches fixes when copilot label is added)   │     │
│   └──────────────────────────┬────────────────────────────┘     │
│                              │                                   │
│                              ▼                                   │
│   scanner picks up the new issue/PR on next cycle               │
│   → state.db updated → fix-loop skill triggered                 │
└─────────────────────────────────────────────────────────────────┘
```

### Important boundary

GitHub Actions mutate **GitHub state** (issues, PRs, labels). The local scanner **polls GitHub** and reflects changes into SQLite. There is no direct connection between GitHub Actions and the local SQLite database. The scanner is the single writer to the DB; GitHub is the source of truth for issue/PR state.

---

## The five components

### 1. launchd agent (scheduler)

A macOS LaunchAgent plist fires `worker.sh` every 15 minutes. See [`launchd/com.hive.scanner.plist.example`](../launchd/com.hive.scanner.plist.example) for the template.

Why launchd instead of systemd: the deployment target is a Mac Mini. The pattern is identical — only the process manager differs.

### 2. worker.sh (scanner)

A ~150-line bash script. See [`worker.sh.example`](worker.sh.example) for a sanitized version.

Each invocation:
1. Acquires a file lock (prevents overlapping cycles)
2. Initializes SQLite tables if missing
3. Starts a new cycle record
4. Scans each repo via `gh issue list` and `gh pr list`
5. Upserts every open item into the `items` table
6. Detects closures (items in DB as "open" but not seen in scan)
7. Sends an ntfy push with the cycle summary
8. Writes a trigger file for the AI agent

No AI, no LLM, no API keys beyond GitHub CLI auth. Runs in <30 seconds even across 6 repos.

### 3. state.db (SQLite)

Three tables — see [`sqlite-state.md`](sqlite-state.md) for the full schema:
- `items`: every tracked issue/PR with status flow (open → triaged → fixing → fixed/closed/skip)
- `cycles`: audit trail of every scan cycle
- `repo_counts`: per-repo counts per cycle (trend data)

### 4. Copilot CLI skills (fix engine)

Three skills in `.claude/commands/`:

| Skill | Purpose |
|---|---|
| `fix-loop.md` | Reads `state.db`, triages open items, fixes everything actionable. The main fix engine. |
| `auto-qa.md` | Discovers and fixes all open auto-qa issues and stalled Copilot PRs. Specialized for CI/quality. |
| `hygiene.md` | Full operational sweep: nightly builds, CI status, PR review, issue review, branch cleanup, deployment health, Helm/brew currency. |

Each skill is a markdown file with structured instructions. The AI agent reads the skill, executes it, and updates the SQLite DB with results. Skills are the "policy files" of this deployment — they just live in `.claude/commands/` instead of the agent's memory directory.

### 5. GitHub Actions workflows (automated responders)

Three workflows that create a feedback loop:

| Workflow | Trigger | Action |
|---|---|---|
| `workflow-failure-issue.yml` | Any workflow fails on main | Files an issue with failure details, error summary, and `workflow-failure` label |
| `ai-fix.yml` | Issue gets `copilot` label | Dispatches Copilot to attempt an automated fix |
| `auto-qa.yml` | Manual or scheduled | Runs the auto-qa skill in CI |

The feedback loop:
1. A workflow fails → `workflow-failure-issue.yml` files an issue
2. The scanner picks up the issue on its next 15-minute cycle
3. The fix-loop skill reads it from the DB and attempts a fix
4. If the fix succeeds, the scanner detects the issue closed and sends ntfy
5. If the workflow self-recovers, the scanner detects the issue closed on GitHub

---

## Results (first 30 days)

- **36+ issues** closed autonomously
- **20+ PRs** merged
- **Median fix time**: ~20 minutes (from issue filed to PR merged)
- **Categories fixed**: test failures, CI config, accessibility gaps, z-index bugs, broken deep-links, stale formulas, doc inaccuracies, nightly workflow failures
- **Zero production incidents** caused by automated fixes
- **6 repos** under continuous management

---

## Lessons learned

### SQLite > beads for this use case

The beads ledger is designed for multi-agent, multi-host coordination with structured actor ownership. This deployment runs on a single machine. SQLite gave us:
- Full SQL for ad-hoc queries and trend analysis
- Zero setup (ships with macOS)
- Trivial schema evolution (just `ALTER TABLE`)
- Sub-millisecond reads even with thousands of rows

We'd use beads if agents spanned multiple machines.

### Skills > policy files for Claude Code

Claude Code's `.claude/commands/` directory is purpose-built for structured instructions. Compared to raw policy files in memory:
- Skills auto-complete with `/` in the CLI
- Skills are version-controlled alongside the project
- Skills can be invoked programmatically (by launchd, by other skills, by the operator)
- No "step 0 re-read" needed — the skill is read fresh every invocation

### GitHub Actions as force multiplier

The most impactful addition was `workflow-failure-issue.yml`. Before this, workflow failures sat unnoticed for days. After: every failure becomes an issue within minutes, the scanner picks it up, the fix-loop fixes it. The human sees a phone notification that says "✅ fixed" and never has to look at the CI dashboard.

### Backoff prevents thrashing

The `fix_attempts` counter in the `items` table is critical. Without it, the fix-loop would retry the same unfixable issue every 15 minutes forever. With it: 3 failed attempts → status='skip', operator gets ntfy, issue stays tracked but stops consuming agent time.

### Separation of scanning and fixing pays off

The scanner runs even when the AI session is down (restarting, usage-limited, rate-limited). State is never lost. When the AI comes back, it reads the DB and knows exactly what happened while it was gone. This resilience was important during Claude Code session restarts, model rate limits, and network issues.

---

## Adapting this for your project

1. **Fork `worker.sh.example`** and change `ORG`, `REPOS`, `NTFY_TOPIC`
2. **Write a fix-loop skill** (or policy file) that reads your `state.db`
3. **Add `workflow-failure-issue.yml`** to your GitHub repo (optional but high-leverage)
4. **Install the scanner plist** — see [`launchd/com.hive.scanner.plist.example`](../launchd/com.hive.scanner.plist.example)
5. Start with a 15-minute scan interval and adjust based on your project's velocity

The scanner and fix-loop are intentionally decoupled. You can run the scanner on Day 1 (just watching, no fixing) and add the fix-loop later when you're confident in the triage rules.
