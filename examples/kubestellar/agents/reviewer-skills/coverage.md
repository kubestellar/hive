# Reviewer Skill: Code Coverage

Load this when checking or fixing test coverage below the 91% target.

## Code Coverage — maintain ≥91% — FIX MANDATORY

**Every pass**, check current test coverage. If below 91%, you MUST actively write tests and open PRs to raise it. **Do NOT just report the gap.** This is your #1 fix obligation.

### How to fix coverage

**Dispatch a background agent** — never run coverage in your main session:

```bash
# Use Agent tool with run_in_background=true
Agent(subagent_type="general-purpose",
      description="Fix coverage below 91%",
      prompt="In /home/dev/kubestellar-console/web, run npm run test:coverage. If below 91%, identify the 3-5 files with worst coverage, write tests, create branch coverage/increase-<timestamp>, git commit -s, push, open PR. Return the PR number and new coverage %.",
      run_in_background=true)
```

Move on to the next health check immediately after dispatching. Do NOT wait for the agent to finish.

### If coverage ≥ 91%

Send simple ntfy: `"Coverage <X>% ✓"`. No further action needed.

### Rules — NON-NEGOTIABLE

- **Do NOT just report low coverage** — write tests and PR them. Reporting without fixing is a policy violation.
- **Do NOT move to the next check** until you've opened a coverage PR or confirmed ≥91%.
- **Do NOT skip silently.** Every pass must either confirm ≥91% or open a PR to move toward it.
- **Re-run coverage after writing tests** to verify actual improvement before opening the PR.
- **Target the biggest gaps first**: sort by uncovered lines, pick 2–5 files with the worst coverage.
- File a bead if coverage has been below 91% for >2 consecutive passes:
  ```bash
  cd ~/reviewer-beads && bd create --title "coverage-gap: Test coverage below 91% for <N> consecutive passes. Current: <X>%." --type bug --priority 2
  ```
