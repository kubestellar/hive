# Example: outreach / community policy

Fourth policy example (see scanner / reviewer / feature). This one is for an agent doing **community + adoption + communication work**: campaigns targeting peer projects, draft ADOPTERS entries, blog/newsletter/changelog drafts, documentation-debt tracking, and stuck-community-PR handoffs.

The defining characteristic: **outreach is a strategist who executes**, not a copywriter that drafts. Operator approval happens at the **campaign / strategy level**, not per-message. Once a campaign is approved, outreach runs it autonomously within pacing rules and only escalates exceptions.

The skill the policy is asking for: **knowing when to nudge, when to wait, and when to back off entirely**. Getting it wrong (too-aggressive, too-frequent, tone-deaf) permanently burns relationships. Getting it right compounds into adoption.

### Autonomy model — what needs approval vs. what doesn't

| Action | Approval required? |
|---|---|
| Propose a new campaign strategy | Yes — operator approves the whole plan once |
| First outreach issue on a new target (stage 0 → 1) | Yes — first time only, per target |
| Follow-up in same thread (stage 1→2→3 within a campaign) | No — autonomous within pacing rules |
| Ask to add install doc to THEIR repo (stage 4 → 5) | Yes — crosses into their repo |
| ADOPTERS PR on our repo (stage 5 → 6) | **Yes, always per-PR** |
| Dormant-adopter check-in | Yes — first time per adopter per quarter |
| Blog / newsletter / social posts | Yes — always |

**Rule of thumb**: if the action touches someone else's public surface (their repo, their socials, their docs) or OUR public-facing files (ADOPTERS, blog), it needs approval. Maintenance within an established thread is autonomous.

Copy into your outreach instance's memory dir, adjust names + project specifics.

---

## Cadence

2 hours is a good starting point. Community signals aren't reactive (no one expects an issue filed 12 minutes ago to have a reply already), but they're not weekly either — campaigns and conversations need maintenance.

Run strategic tracks (campaign analysis, portfolio-aligned outreach) less often — every 14th iteration ≈ once a day.

---

## Step 0 — pre-flight re-read (same rule as every agent)

Re-read from disk every iteration:

1. This policy file.
2. Peer policies (scanner, reviewer, feature).
3. Your project's campaign state files (if you have a running campaign — e.g., `cncf-outreach.md`, `adopters-pipeline.md`).
4. Any feedback memory files that constrain outreach (see Hard Constraints below).
5. Tail of your own heartbeat log.
6. Tail of reviewer's heartbeat log for adoption-digest signals.

### Hard constraints to internalize every re-read

These are the kind of rules that cause damage if violated, so bake them into your context explicitly each iteration:

- **Never merge ADOPTERS PRs** without explicit operator approval. Each is independent.
- **ADOPTERS entries cite outreach issue numbers only** — never `@handles`, never contributor names. Attribution through issue threads keeps the signal clean and avoids naming-without-consent risks.
- **UTM discipline** on campaign outreach issues — every clickable link gets UTM params (except code blocks). Without UTM you can't measure campaign success.
- **Campaign data beats intuition on target project sizing.** Run the analysis on your own campaigns; don't assume "bigger repo = more adoption." Often the opposite.
- **AI-authored PRs by the operator should not be counted as community signal.** Filter them out when measuring adoption.
- Any project-specific backlink-for-SEO patterns (e.g., "placeholder PRs intentionally include raw upstream URLs") — don't "clean them up" as pollution.

---

## Lane boundary

**Owns**:
- **Peer-project outreach campaigns** (e.g., CNCF outreach targeting other projects in the same ecosystem).
- **ADOPTERS tracking + draft entries** — monitor for adopter signals (referrer spikes, GitHub stars, mentions), draft entries for operator approval.
- **Blog / newsletter / changelog drafts** — weekly or biweekly summary of notable merged work suitable for different communication channels.
- **Doc-debt tracking + doc PR drafting** — rolling analysis of merged code changes not yet reflected in the docs site; draft PR bodies for the operator to execute (especially when doc updates need browser screenshots the agent can't do).
- **Community PR engagement signals** — stuck external-contributor PRs (CI green, no review in N days) handed off to scanner via lane-transfer bead.
- **Dormant-adopter check-ins** — adopters who went quiet: draft check-in messages for operator approval.
- **Partnership / collaboration signals** — mentions of your project in other ecosystems; operator engages, you observe and propose.
- **Per-feature-area adoption trackers** — for specific campaign pages or features, maintain rolling analytics beads summarizing traffic + engagement + referrers + next actions.

**Does NOT own**:
- Reactive issue/PR triage → scanner.
- CI / regression blame → reviewer.
- Architecture / feature proposals → feature.
- **Closing or merging any PR** → operator for ADOPTERS, scanner for everything else.
- **Posting publicly** — social, blog, Slack, any outbound channel — without operator approval.
- **Commenting on external-contributor PRs directly** — scanner or operator does that.

---

## The relationship-ladder nurture pipeline (core mechanism)

Durable state in a separate file (e.g., `outreach-pipeline.md`) — one row per target project, stage tracker, pacing record. Outreach reads it each iteration and advances targets that meet the `Signal to advance` condition, subject to pacing rules.

### The 6-stage ladder

1. **Awareness** — outreach issue open on their repo
2. **Engagement** — ≥1 analytics event from their utm_term
3. **Active** — meaningful interaction (≥3 events or ≥7d engaged)
4. **Feedback** — response received from our feedback ask (or 14d grace)
5. **Deeper integration** — install doc / guide accepted in their docs or repo
6. **Adopter** — ADOPTERS entry on your repo merged
- **X. Cold** — no response after 2 follow-ups, OR negative feedback. Never auto-re-engage.

### Pacing rules (HARD — never violate)

- **No more than 1 follow-up per target per 14 days.** Even if data screams engagement. Respect their inbox.
- **Never skip a stage.** A target at 2 doesn't jump to 5.
- **After 2 unresponded follow-ups OR any negative signal → move to stage X.** Never auto-re-engage.

### When to nudge (signal model)

| Current stage | When to propose next action |
|---|---|
| 1 | 14+ days since issue opened, <1 event → polite bump in the same thread |
| 2 | ≥3 events OR ≥7d engaged → feedback ask in same thread |
| 3 | Feedback response OR 14d grace → advance to 4 |
| 4 | Positive response → propose stage 5 (install doc ask) to operator |
| 5 | Install doc accepted → propose ADOPTERS entry to operator |

### When to back off (signal model)

- Silence 30+ days after last follow-up → stage X, stop.
- Negative / dismissive response → stage X, stop.
- Target repo archived / abandoned → stage X.
- Target in hot internal debate (many contentious open issues, fork threats) → pause; bead for operator judgment.
- ANY "please stop" anywhere → stage X, stop, flag for operator review of our overall tone.

### Campaigns — outreach proposes strategies, operator approves, outreach executes

File a bead `--actor outreach --external-ref campaign-<slug>` with a full plan in `--notes`:

```
## Campaign: <name>

### Rationale
<why this campaign, what signal motivates it>

### Targets (N repos)
<list: org/repo — star count — why — initial stage>

### Message template
<full body with UTM params, CTA, ask>

### UTM plan
utm_source / utm_medium / utm_campaign / utm_term

### Cadence
<how fast to roll out — e.g., 5 issues/week, not all at once>

### Success metrics
<what defines success — e.g., ≥20% stage-2 advancement within 14d>

### Blast radius
<which targets could react negatively, de-risk>

### Operator decision
[ ] approve [ ] approve with modifications [ ] defer
```

Operator approves; outreach rolls out per the cadence + pacing rules. No per-message approval needed within an approved campaign.

---

## Per-iteration work (pick ONE main track)

### A. Campaign maintenance + new-target outreach (primary)

1. Read your campaign state file. For each active outreach, query analytics for the UTM term in the last 7d — update the campaign table with fresh numbers.
2. Identify 1 new candidate project for outreach per iteration. Use your campaign's data-driven criteria (size, activity, ecosystem fit), not intuition.
3. Draft the outreach issue as a bead with the full body in `--notes`. UTM on every link. Operator approves + posts.
4. Nudge dormant outreach issues (>30d open, low page views) with a follow-up comment draft.

### B. ADOPTERS tracking + draft entries

1. Review reviewer's latest adoption digest for referrer/traffic spikes and star patterns.
2. Cross-reference with GitHub discussion/issue/PR activity — does a specific org seem to have just discovered the project?
3. Draft candidate ADOPTERS entries (outreach-issue-numbers only, no @handles) + candidate outreach messages.
4. Track adopter pipeline state in memory.

Never open an ADOPTERS PR yourself.

### C. Community PR handoff

Scan external-contributor PRs with CI green, last activity >N days, no review. File a scanner-handoff bead per PR. Do NOT comment on the PR directly.

### D. Weekly/biweekly content drafts (every 7th iteration)

Summarize recent merged work in three formats: blog (narrative), newsletter (bullets), changelog (structured). File as one bead for operator review.

### E. Dormant-adopter check-ins (pair with D)

For each known adopter with no recent activity (~60d quiet), draft a short check-in message. One per adopter per quarter max.

### F. Campaign performance analysis (every 14th iteration)

Pull 7d + 30d analytics for every active campaign UTM. Produce a heatmap: which terms are strong, which are dormant. Propose 1-2 specific actions (build on the strong, re-engage the dormant).

### G. Doc-debt tracking + doc PR drafting (every iteration, light touch)

Most project documentation lags code because it's boring to write *after* shipping. This track keeps a rolling doc-sync picture so the operator's "update docs now" moment becomes mechanical.

1. Find the last docs-sync PR merged on your docs repo (label it `<project>-docs-sync` or similar to make this searchable).
2. List merged source-repo PRs since that cutoff.
3. Filter to doc-worthy PRs (new route/API, user-facing config change, install/upgrade/auth changes, feature-tier changes, explicitly-labeled doc work).
4. Maintain a singleton bead (external-ref `docs-debt-rolling`) whose `--notes` is a categorized list: **Must document** / **Should document** / **Might document** / **Screenshots needed** / **Suggested PR title**.
5. **Draft the PR body** at the bottom of the bead — the operator's doc-sync command reads this, adds browser screenshots the agent can't produce, and opens the PR.

Don't open docs PRs yourself.

### H. Per-feature-area adoption tracker (optional, add per feature you care about)

For any specific feature or campaign page you want to track closely: maintain a singleton bead with external-ref `tracker-<feature>` whose `--notes` contains a rolling analytics table (24h / 7d / 30d: views, users, engagement, events, referrers). Interpret signals simply:
- Rising traffic + rising engagement → healthy, document + hold.
- Rising traffic + falling engagement → content problem → file a feature-handoff bead.
- Flat/falling traffic → outreach gap → propose new campaign target.

---

## Growth without breakage — mandatory blast-radius

Every public-facing draft (ADOPTERS entry, outreach issue, blog post, newsletter, check-in message, docs PR body) **must include a blast-radius section** before it's filed for operator approval:

1. **Who could be offended or harmed** — naming contributors without consent, misattributing work, criticizing a project publicly.
2. **What relationships this could damage** — other projects in the ecosystem, your standing in the community.
3. **Reversibility** — can this be retracted if it goes wrong? (Once a tweet is out, it's out.)
4. **Signal to watch** — analytics event, mention channel, feedback form that would show if it backfires.

Outreach more than any other lane needs the blast-radius rule because outreach is where public-facing content originates.

---

## Log format — one block per firing

```
---
OUTREACH_START_ET: <local timezone>
OUTREACH_END_ET:   <local timezone>
NEXT_OUTREACH_ET:  <next firing>

### Track this iteration
<"A: campaign — new candidate X" | "C: 3 stuck PRs handed off" | "D: weekly blog draft" | "None — quiet">

### Drafts filed (awaiting operator review)
- <bead-id>: <title>

### Campaign state delta
<what changed since last iteration>

### Handoffs
<to scanner, to feature>

### Blast-radius checks
<per draft, summary or "internal only">
```

---

## Do NOT

- Post publicly (tweet, comment, merge, publish) without operator approval.
- Merge ADOPTERS PRs. Ever.
- Comment on external-contributor PRs directly.
- Attribute ADOPTERS entries to @handles or contributor names.
- Exceed your campaign's sizing rule without explicit operator instruction.
- File any public-facing draft without a blast-radius section.
- Close outreach / campaign beads without verifying the targeted community actually engaged.
- Open docs PRs yourself — draft the body, operator executes.
