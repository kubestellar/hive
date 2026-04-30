# Reviewer Skill: GA4 Error Watch

Load this when checking GA4 error rates, filing GA4 error issues, or identifying instrumentation gaps.

## GA4 Error Watch — CRITICAL

GA4 errors are your highest-priority check. Every pass MUST include GA4 error analysis.

**What to check:**
- New error classes in the last 30 min vs 7-day baseline
- Trending errors: any error >3× its baseline rate
- `login_failure` spikes
- Uncaught exceptions, chunk load failures, API errors
- Any error pattern that correlates with a recent PR merge

**When errors are found — ALWAYS open an issue:**

```bash
unset GITHUB_TOKEN && gh issue create --repo ${PROJECT_PRIMARY_REPO} \
  --title "🐛 GA4 error: <error class or pattern>" \
  --label "bug,ga4-error" \
  --body "## GA4 Error Report

**Error class:** <name>
**Rate:** <count> in last 30min (baseline: <count>/30min over 7d)
**Trend:** <increasing/spike/new>
**First seen:** <timestamp>
**Affected pages:** <paths if known>
**Correlated PRs:** <recent merges if relevant>

## Raw data
<paste the relevant GA4 table rows>

## Suggested investigation
<what to look at — stack traces, affected components, recent changes>"
```

Send high-priority ntfy for every GA4 error issue filed.

**Do NOT skip this.** Do NOT just log errors to reviewer_log.md without filing issues. Every error that exceeds baseline gets an issue.

## GA4 instrumentation gaps

If you find that GA4 is not capturing enough detail to diagnose an error or make a decision (e.g., missing custom dimensions, no error stack traces, no page context, missing user flow events, no A/B variant tracking), open an issue:

```bash
unset GITHUB_TOKEN && gh issue create --repo ${PROJECT_PRIMARY_REPO} \
  --title "📊 GA4 gap: <what's missing>" \
  --label "enhancement,ga4-instrumentation" \
  --body "## Missing instrumentation

**What I was trying to determine:** <the question you couldn't answer>
**What data is available:** <what GA4 currently reports>
**What's missing:** <specific events, dimensions, or properties needed>
**Impact:** <what decisions this blocks — error triage, adoption analysis, etc.>

## Suggested fix
<specific GA4 events or custom dimensions to add, where in the code>"
```

For straightforward instrumentation gaps (adding a GA4 event, custom dimension, or error context), you MAY also spawn a background fix agent to implement the fix immediately.

## GA4 Output Rule

When running the GA4 adoption digest or error watch, **print all tables and the Mermaid chart directly to your output** — do not only write them to reviewer_log.md. The supervisor watches this tmux pane and needs to see the numbers live. Always do both: write to log AND print to stdout.
