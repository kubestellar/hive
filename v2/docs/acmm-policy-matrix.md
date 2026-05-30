# ACMM Policy Matrix

## Policy Modes

Each agent runs in one of four modes, controlling what actions it can take on GitHub.

| Mode | Suffix | Beads | Issues | PRs | Merge | GH Auth |
|------|--------|-------|--------|-----|-------|---------|
| **Advisory** | `-advisory.md` | Yes | No | No | No | No |
| **Measured** | `-measured.md` | Yes | Yes | No | No | Yes |
| **Holdgated** | `-holdgated.md` | Yes | Yes | Yes + `hold` label | No | Yes |
| **Full** | `-full.md` | Yes | Yes | Yes | Yes (auto on green CI) | Yes |

- **Advisory**: Agent observes and records findings as beads on the dashboard. No GitHub interaction.
- **Measured**: Agent can file GitHub issues to make findings visible to the team. No code changes.
- **Holdgated**: Agent can write code and open PRs, but every PR gets a `hold` label. A human must review and remove `hold` before merge. Agent never merges.
- **Full**: Agent operates autonomously — opens PRs and merges on green CI. Highest trust level.

## ACMM Levels

### L1 — Assisted (1 agent)

A single interactive advisor helps with repo setup and architecture decisions.

| Agent | Mode | Template |
|-------|------|----------|
| guide | advisory | `guide-advisory.md` |

### L2 — Instructed (4 agents)

Agents observe and report findings as advisory beads. No GitHub issues or PRs. Humans decide what to act on.

| Agent | Mode | Template |
|-------|------|----------|
| supervisor | no-github | `supervisor-nogithub.md` |
| scanner | advisory | `scanner-advisory.md` |
| quality | advisory | `quality-advisory.md` |
| guide | advisory | `guide-advisory.md` |

### L3 — Measured (5 agents)

Quality opens issues AND hold-gated PRs about testing gaps. All other agents remain advisory. CI-maintainer joins to monitor build health.

| Agent | Mode | Template |
|-------|------|----------|
| supervisor | no-github | `supervisor-nogithub.md` |
| scanner | advisory | `scanner-advisory.md` |
| ci-maintainer | advisory | `ci-maintainer-advisory.md` |
| **quality** | **holdgated** | `quality-holdgated.md` |
| guide | advisory | `guide-advisory.md` |

### L4 — Adaptive (6 agents)

All agents open GitHub issues. Quality, ci-maintainer, and sec-check may also open hold-gated PRs. Security agent joins. Closed-loop feedback: agents act on their own findings.

| Agent | Mode | Template |
|-------|------|----------|
| supervisor | no-github | `supervisor-nogithub.md` |
| scanner | measured | `scanner-issues.md` |
| ci-maintainer | holdgated | `ci-maintainer-holdgated.md` |
| **quality** | **holdgated** | `quality-holdgated.md` |
| guide | measured | `guide-issues.md` |
| **sec-check** | **holdgated** | `sec-check-holdgated.md` |

### L5 — Semi-Automated (8 agents)

All agents open issues AND hold-gated PRs. Architect produces RFCs, strategist coordinates across agents. The system proposes; it does not merge autonomously.

| Agent | Mode | Template |
|-------|------|----------|
| supervisor | no-github | `supervisor-nogithub.md` |
| scanner | holdgated | `scanner-holdgated.md` |
| ci-maintainer | holdgated | `ci-maintainer-holdgated.md` |
| quality | holdgated | `quality-holdgated.md` |
| guide | holdgated | `guide-holdgated.md` |
| sec-check | holdgated | `sec-check-holdgated.md` |
| architect | holdgated | `architect-holdgated.md` |
| strategist | holdgated | `strategist-holdgated.md` |

### L6 — Fully Autonomous (9 agents)

Agents open issues, create PRs, and auto-merge on green CI. No hold label. Outreach agent handles community engagement. Governor at fastest cadence.

| Agent | Mode | Template |
|-------|------|----------|
| supervisor | no-github | `supervisor-nogithub.md` |
| scanner | full | `scanner-automerge.md` |
| ci-maintainer | full | `ci-maintainer-full.md` |
| quality | full | `quality-full.md` |
| guide | full | `guide-full.md` |
| sec-check | full | `sec-check-full.md` |
| architect | full | `architect-full.md` |
| strategist | full | `strategist-full.md` |
| outreach | full | `outreach-full.md` |

## Key Rules

1. **All PRs are holdgated below L6.** No agent can auto-merge unless running at L6 (Fully Autonomous).
2. **Advisory agents never get GH auth.** The `${GH_AUTH}` template variable is only injected into measured, holdgated, and full templates.
3. **Supervisor never touches GitHub.** At every level, supervisor uses `supervisor-nogithub.md` — it monitors agent health, not code.
4. **Mode escalation is per-agent.** At L4, some agents are measured (issues only) while others are holdgated (issues + PRs). The level defines the mix.
5. **Knowledge priming works at all levels.** The `${KNOWLEDGE}` template variable injects relevant facts from git sources and wiki layers regardless of the agent's mode.
