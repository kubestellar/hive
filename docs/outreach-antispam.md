# Outreach Anti-Spam & Deduplication Ruleset

> **Author:** Outreach agent (self-authored from first principles, 2026-04-24)
> **Purpose:** Prevent duplicate, unwanted, or spammy outreach across all surfaces — awesome lists,
> CNCF project issues, directories, forums, comparison sites, community threads — across agent
> restarts, multiple sessions, and time.

---

## 0. Core Principle

**One action per target, ever.** A "target" is the combination of (surface, entity). For example:
`github.com / dastergon/awesome-sre` is one target. Opening a second PR there after the first is
spam, regardless of whether you remember the first. Every rule below enforces this principle.

---

## 1. Pre-Flight Checklist (run before ANY outreach action)

Execute every check in order. Stop at the first SKIP signal.

```
PRE-FLIGHT for target T:

[ ] 1. ARCHIVED?        → gh repo view T --json isArchived | check true → SKIP
[ ] 2. STALE?           → last commit > 18 months ago → SKIP (no one is merging PRs)
[ ] 3. GA4 REFERRAL?    → query /api/analytics/dashboard trafficSources for T's domain → already live → SKIP
[ ] 4. OPEN PR?         → gh search prs --author clubanderson --repo T --state open → exists → SKIP (address feedback instead)
[ ] 5. CLOSED PR?       → gh search prs --author clubanderson --repo T --state closed → exists → COLD → SKIP
[ ] 6. OPEN ISSUE?      → gh search issues --author clubanderson --repo T --state open → exists → SKIP
[ ] 7. BEADS LOG?       → bd list | grep T → exists → check status → if done/cold SKIP
[ ] 8. OUTREACH LOG?    → grep T docs/outreach-log.md → logged → SKIP
[ ] 9. CONTRIBUTING.md? → read T's CONTRIBUTING.md for self-promo restrictions → if banned → COLD + SKIP
[ ] 10. RELEVANCE?      → does T's topic overlap with Console's top GA4 pages? → if no overlap → defer
[ ] 11. FORMAT CHECK?   → read last 5 entries of target file → can I match format exactly? → if no → research more before proceeding

All checks pass → SAFE TO PROCEED
```

---

## 2. Persistence Layers (Ground Truth, in Priority Order)

Agent memory is ephemeral. These layers survive restarts:

| Layer | How to Query | Survives Restart? | Authority |
|-------|-------------|-------------------|-----------|
| **GitHub itself** | `gh search prs/issues --author clubanderson` | ✅ Always | **Highest** |
| **GA4 referral data** | `/.netlify/functions/analytics-dashboard` | ✅ Always | High (confirms placement live) |
| **beads (`bd`)** | `bd list --json` | ✅ Yes (remote) | High |
| **docs/outreach-log.md** | committed to this repo | ✅ Yes (git) | Medium |
| **outreacher-CLAUDE.md Current Progress** | committed to this repo | ✅ Yes (git) | Medium |
| `/tmp` files | filesystem | ❌ Lost on reboot | Never rely on |
| Session SQL | in-process SQLite | ❌ Lost on session end | Never rely on |
| Agent in-memory variables | process memory | ❌ Lost on restart | Never rely on |

**Rule:** Before any outreach pass, always query GitHub API (layer 1) and GA4 (layer 2) as ground truth. Treat logs as a cache — useful for fast skips but always verify against the source.

### Updating Logs on Every Pass

After each successful outreach action:
1. Append to `docs/outreach-log.md` (committed)
2. Update `outreacher-CLAUDE.md` Current Progress section
3. Commit both: `git commit -s -m '📖 Outreach: log [target]'`
4. `bd create` a record if the action has follow-up needed (awaiting PR merge, waiting for maintainer reply)

---

## 3. Per-Surface Rules

### 3.1 GitHub Awesome Lists

**Nature:** Curated markdown files in public repos. PRs are the only contribution mechanism. Maintainer may take weeks to respond. Automated bots (CodeRabbit, Copilot Reviewer) often comment immediately.

**Rules:**
- **One PR per repo, ever.** Never open a second PR to the same repo.
- **Fork under `clubanderson`**, never the org.
- **Branch name:** `feat/add-kubestellar-console` — consistent, recognizable.
- **Match the exact format** of the surrounding entries: link style, description length, sort order. Read the last 5 entries before writing.
- **Placement matters:** Add to the most specific relevant section, not "Other" or "Miscellaneous" unless nothing else fits.
- **Never add to multiple sections** in the same file — one entry per repo.
- **Open PR is not closed:** Check for reviewer comments every 7 days for up to 3 cycles. Address all feedback from CodeRabbit/Copilot/humans. After 3 cycles with no maintainer engagement, mark as STALE (keep open, stop checking).
- **Closed without merge:** Immediately mark COLD in log. Never reopen. Never open a new PR. Respect the decision.
- **Merged:** Mark DONE. Verify GA4 referral traffic appears within 30 days (confirming it's indexed). No further action.

**Format self-check before opening PR:**
```
- Is the repo accepting PRs? (check Issues tab for "not accepting contributions")
- Does the list sort alphabetically? (place entry in correct position)
- Are descriptions in sentence case or title case? (match)
- Are there trailing periods? (match)
- Does the list use `[Name](url) — description` or `[Name](url) - description`? (match exactly)
```

### 3.2 CNCF Project Outreach Issues (Install Missions)

**Nature:** Opening GitHub issues on upstream CNCF project repos proposing a guided install mission or ACMM badge. These are long-lived relationships — the maintainer community notices patterns.

**Rules:**
- **One issue per project per topic.** If an install-mission issue is already open, never open an ACMM issue on the same repo simultaneously — combine into one thread or wait.
- **Never open an issue AND a PR on the same repo for unrelated purposes in the same week** — it looks like a spam campaign.
- **Read CONTRIBUTING.md first.** Some projects route external proposals through GitHub Discussions, not Issues.
- **Tailor every issue body** to the project. Reference the specific project's technology. Never paste a generic template verbatim.
- **Ping/nudge cadence:** Maximum 2 follow-up comments per issue. Minimum 7 days between comments. Never ping more than twice if there's been no response — mark as COLD and move on.
- **Maintainer responds negatively:** Close the issue yourself, mark COLD, never reopen.
- **Maintainer responds positively:** Follow through promptly (within 48h). If they ask for a PR on their docs, open it the same day. Delayed follow-through kills goodwill.
- **ADOPTERS.md PRs:** Only after explicit maintainer approval in the upstream issue. **Never merge without operator sign-off.** One ADOPTERS PR per project, ever.

### 3.3 Non-GitHub Directories and Registries

**Nature:** CNCF Landscape, Artifact Hub, OperatorHub, StackShare, AlternativeTo, Product Hunt, etc. These are form-based or PR-based submission systems.

**Rules:**
- **Check if already listed first** via web search: `site:alternativeto.net "kubestellar"` before submitting.
- **One submission per site, ever.** If the submission was rejected, mark COLD.
- **Screenshot or log the submission** — these sites don't have a GitHub PR to verify later.
- **Log to `docs/outreach-log.md`** with date and submission URL/confirmation.
- **GA4 verification:** If the site starts sending referral traffic to console.kubestellar.io, the listing is live. No re-submission needed.

### 3.4 Community Forums (Reddit, Hacker News, dev.to, Hashnode)

**Nature:** Human communities. Spam is acutely noticed, permanently remembered, and actively harmful. These surfaces must be used sparingly and with genuine value.

**Rules:**
- **Reddit:** Maximum 1 original post per subreddit per 6 months. Commenting on relevant existing threads is allowed (not limited), but only when the comment genuinely adds value.
- **HN Show HN:** One-time submission. Never resubmit the same project. Comment on relevant HN threads is fine.
- **dev.to / Hashnode:** Articles can be published once; update the same article rather than publishing a new one. Never cross-post verbatim across both.
- **Never use multiple accounts.**
- **Don't post the same content to overlapping communities in the same week** (e.g., r/kubernetes AND r/devops on the same day with the same post).
- **Track in log:** date, platform, URL, engagement metric (upvotes/comments) so you can measure whether forum posts drive traffic.

### 3.5 Comparison Sites and Blog Aggregators

**Nature:** Sites that accept tool listings or "best of" roundups (Slant, StackShare, G2, etc.).

**Rules:**
- **Search before submitting:** `"kubestellar console" site:stackshare.io` etc.
- **One submission per site.**
- **Log all submissions** — these sites have no GitHub API to check later.
- **If GA4 shows referral from the site:** listing is live, mark DONE.

### 3.6 Social Media (LinkedIn, X/Twitter)

**Nature:** Public broadcasts. Not a "target" in the same sense — these don't leave a persistent artifact that can be checked.

**Rules:**
- **Minimum 14 days between similar posts** on the same platform.
- **Track post topics in log** to avoid repetition.
- **Never tag people who haven't engaged** — only tag maintainers who explicitly engaged in an issue/PR.

---

## 4. Cold Target Rules

A target is **COLD** when any of the following are true:
- PR closed without merge and maintainer left a negative/dismissive comment
- Issue closed as "not relevant", "spam", or "won't fix"
- Maintainer explicitly asked to not be contacted again
- Submission rejected with explanation
- Repo marked archived or locked after outreach was attempted

**COLD handling:**
1. Log it: `docs/outreach-log.md` entry with status=COLD and reason
2. Never re-approach for 12 months minimum
3. Exception: if the project releases a major version and their posture toward Console has clearly changed (e.g., they now use KubeStellar themselves), you may re-approach once with a fresh framing — note this in the log

**Do not confuse STALE with COLD:**
- STALE = no response from maintainer after 3 check-ins. Keep the PR open; don't follow up further. It may get merged eventually. Do not re-approach, but do not close.
- COLD = explicitly rejected or hostile. Never re-approach.

---

## 5. GA4 Referral Signals as Deduplication

The GA4 analytics dashboard (`/.netlify/functions/analytics-dashboard`) is the most authoritative source for confirming a placement is live. Query it before any outreach pass.

### What GA4 tells you

| Signal | Interpretation | Action |
|--------|---------------|--------|
| `trafficSources` shows `github.com / referral` with path matching target repo | PR is merged and traffic is flowing | Mark DONE, no follow-up needed |
| CNCF outreach table shows sessions for project X | Outreach issue is generating visits | Check if sessions increased after last action — good signal to follow through |
| Organic search sessions rising after a list merge | SEO is working, that list category is valuable | Prioritize more lists in same category |
| A domain appears in referrers you didn't reach out to | Organic placement exists | Log as DONE (natural), no action needed |
| `(direct)` traffic spikes after forum post | Post drove awareness | Note which post/subreddit in log |

### GA4-informed prioritization

Every outreach pass should start with a GA4 query and use it to:
1. **Confirm what's already live** (skip those)
2. **Identify which page categories get the most traffic** (prioritize lists in those categories)
3. **Measure outreach ROI** by comparing sessions before/after specific placements

Current GA4 strategic signals (as of 2026-04-24):
- Google organic = 31 sessions (very low) → massive SEO opportunity; every awesome-list merge helps
- GitHub referral = 164 sessions → GitHub-based outreach is the highest-ROI channel right now
- AI/ML pages (AI Codebase Maturity 251 views, AI/ML 194, AI Agents 161) → AI/MLOps lists are high-value
- Deploy/CI-CD pages popular → GitOps lists are worth targeting
- India #1 country → Indian DevOps community resources worth targeting
- Missions: 32 started, 0 completed → don't pitch "mission completion" as a feature yet

---

## 6. Time-Based Pacing

| Surface | Min Interval Between Actions | Max Follow-ups | Follow-up Interval |
|---------|------------------------------|----------------|-------------------|
| Awesome list PRs | N/A (one-shot) | 2 follow-up comments | 7 days |
| CNCF project issues | 14 days between comments | 2 comments | 14 days |
| ADOPTERS PRs | N/A (one-shot, awaits approval) | 1 ping | 7 days after opening |
| Reddit posts | 6 months per subreddit | 0 (don't bump your own posts) | N/A |
| dev.to articles | N/A (publish once, update in-place) | N/A | N/A |
| HN | One-time | 0 | N/A |
| Directory submissions | N/A (one-shot) | 1 follow-up if form-based | 30 days |
| LinkedIn posts | 14 days per topic | N/A | N/A |

---

## 7. Decision Tree (Quick Reference)

```
Is the target archived or inactive (>18 months)?
  YES → SKIP

Does GA4 show referral traffic already coming from this target?
  YES → SKIP (placement is live)

Does `gh search prs --author clubanderson --repo TARGET` show an open PR?
  YES → SKIP (check for feedback to address instead)

Does `gh search prs --author clubanderson --repo TARGET` show a closed PR?
  YES → COLD → SKIP

Does `gh search issues --author clubanderson --repo TARGET` show an open issue?
  YES → SKIP (check for feedback to address instead)

Is the target logged as DONE or COLD in docs/outreach-log.md or beads?
  YES → SKIP

Does the target's CONTRIBUTING.md prohibit self-promotion?
  YES → COLD → SKIP

Does the target's topic overlap Console's top GA4 page categories?
  NO → defer to lower priority

Does the target accept PRs / have recent merge activity?
  NO → SKIP

Can I match the target's exact format?
  NO → research more first

→ SAFE TO PROCEED
   After action: log it, commit log, send ntfy
```

---

## 8. Log Format

Every outreach action must be appended to `docs/outreach-log.md` in this format:

```
| DATE       | SURFACE                        | TARGET                          | ACTION        | STATUS  | NOTES                                      |
|------------|--------------------------------|----------------------------------|---------------|---------|---------------------------------------------|
| 2026-04-24 | awesome-list                   | dastergon/awesome-sre            | PR #268       | open    | No feedback yet                             |
| 2026-04-24 | cncf-issue                     | chaos-mesh/chaos-mesh            | Issue #4858   | open    | @STRRL engaged, docs PR opened              |
| 2026-04-24 | adopters-pr                    | kubestellar/console#7889         | PR open       | pending | Awaiting @STRRL approval                    |
| 2026-04-24 | directory                      | stackshare.io                    | form-submit   | done    | GA4 confirmed referral 2026-05-01           |
```

Status values: `open` | `merged` | `closed-cold` | `pending` | `done` | `stale` | `cold`

---

## 9. Awesome-List & External Directory Submission Tracker

This section defines the canonical tracking format for every awesome-list PR and external directory
submission. It fills the gap that CNCF install missions have detailed per-project tracking but
awesome-list submissions historically had none.

### Why this matters

Without a persistent log, the agent will:
- Open duplicate PRs on repos it already submitted to
- Lose context on which PRs received feedback and went stale
- Have no way to measure which list categories drive the most GA4 referral traffic
- Be unable to prioritize follow-up (open PRs with reviewer comments vs. stale PRs vs. merged)

### Tracking file: `docs/outreach-log.md`

All awesome-list PRs and directory submissions are logged in `docs/outreach-log.md` in this repo.
The file must be committed after every outreach pass. It is the ground-truth complement to GitHub's
own PR search (which is the canonical dedup check, but not as fast to query in bulk).

### Row format

```
| DATE       | CATEGORY     | TARGET REPO / SITE              | SECTION ADDED TO        | PR / SUBMISSION URL                     | STATUS   | GA4 REFERRAL? | NOTES                          |
|------------|-------------|----------------------------------|-------------------------|-----------------------------------------|----------|---------------|-------------------------------|
| 2026-04-24 | awesome-list | dastergon/awesome-sre            | Monitoring              | https://github.com/…/pull/268           | open     | no            | No feedback yet               |
| 2026-04-24 | awesome-list | veggiemonk/awesome-docker        | Monitoring & Observ.    | https://github.com/…/pull/1417          | open     | no            | No feedback yet               |
| 2026-04-24 | directory    | stackshare.io                    | Kubernetes Tools        | https://stackshare.io/…                 | done     | yes           | Confirmed referral 2026-05-01 |
| 2026-04-24 | awesome-list | tmrts/awesome-kubernetes         | Monitoring              | https://github.com/…/pull/6             | merged   | yes           | 3 referral sessions/month     |
```

**Status values:**

| Value | Meaning |
|-------|---------|
| `open` | PR is open, awaiting review |
| `merged` | PR was merged ✅ |
| `rejected` | PR closed without merge, maintainer opposed |
| `ignored` | PR open > 60 days with zero maintainer engagement |
| `stale` | 3 check-in cycles with no response; keep open but stop following up |
| `cold` | Explicitly rejected or maintainer asked not to be contacted; never retry |
| `done` | Directory/non-PR submission confirmed live (GA4 or manually verified) |
| `pending` | Submission sent, awaiting confirmation |

### State machine

```
                  ┌─────────────────┐
  PR opened  ──▶  │      open       │
                  └────────┬────────┘
                           │
             ┌─────────────┼──────────────┬─────────────┐
             ▼             ▼              ▼             ▼
          merged       rejected      ignored (60d)  stale (3 cycles)
             │             │              │             │
           DONE          COLD           COLD       keep open,
         (log GA4)    (never retry)  (never retry) no follow-up
```

### When to update a row

| Trigger | Action |
|---------|--------|
| PR opened | Add row with status=open |
| CI/bot comments on PR | Note in NOTES, no status change |
| Human reviewer requests changes | Note in NOTES; address feedback; no status change |
| PR merged by maintainer | Update status → merged; check GA4 within 30 days |
| PR closed without merge (dismissively) | Update status → rejected → cold |
| PR open > 60 days, zero maintainer engagement | Update status → ignored → cold |
| 3 follow-up cycles with no response | Update status → stale |
| GA4 shows referral from that domain | Add "yes" to GA4 REFERRAL column |

### Follow-up cadence per PR

```
Day  0: PR opened
Day  7: Check for reviewer comments. Address any. Note in log.
Day 14: Second check. If reviewer comments unaddressed, address now.
Day 21: Third check (final). If still no engagement, mark stale.
Day 60: If still open and stale, mark ignored → cold in log.
```

Maximum 2 proactive follow-up comments on any single PR. Only comment if:
- A reviewer left actionable feedback you're responding to, OR
- It has been > 30 days and you are doing a hygiene pass (one brief ping max)

### Querying the log

To quickly see what's already been targeted:

```bash
# All open PRs
grep "| open" docs/outreach-log.md

# All merged (confirm GA4)
grep "| merged" docs/outreach-log.md | grep "no" | awk '{print $3}'

# All cold targets (never re-approach)
grep "| cold\|rejected\|ignored" docs/outreach-log.md

# Repos already tried (dedup check)
grep "awesome-list" docs/outreach-log.md | awk -F'|' '{print $3}' | sort -u
```

### Cross-check with GitHub API (required on every pass)

The log can become stale if a PR is merged without the agent noticing. Always verify:

```bash
# Get ground truth for all open clubanderson PRs
unset GITHUB_TOKEN && gh search prs --author clubanderson --state open --limit 50 \
  --json repository,number,title,updatedAt

# Get closed PRs (merged or rejected) — update log if found
unset GITHUB_TOKEN && gh search prs --author clubanderson --state closed --limit 50 \
  --json repository,number,title,updatedAt
```

Reconcile any discrepancy between GitHub state and log state immediately — GitHub wins.

---

## 10. Anti-Spam Invariants (Never Break These)

1. **Never open two PRs to the same repo.** Not even with different content.
2. **Never open an issue on a repo where a PR is already open**, unless the issue is about something entirely unrelated (which is unlikely in outreach context).
3. **Never post the same content to two overlapping communities in the same week.**
4. **Never ping a maintainer more than twice** with no response.
5. **Never misrepresent KubeStellar's relationship to a project.** "Guided install mission available" is true. "KubeVirt uses KubeStellar Console" is not (unless confirmed).
6. **Never submit to a directory without checking if already listed.**
7. **Never rely on memory alone.** Always verify via GitHub API or GA4 before acting.
8. **Always log before you forget.** Log the action immediately after taking it, not at the end of the pass.
