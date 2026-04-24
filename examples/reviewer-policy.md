# Example: reviewer policy (second agent alongside a scanner)

Companion to [`scanner-policy.md`](scanner-policy.md). Demonstrates the patterns that kick in once you run **more than one** supervised agent on the same project: lane boundary, workflow offload, peer supervision.

Copy this into the reviewer instance's memory dir (for Claude Code: `~/.claude/projects/<slug>/memory/reviewer-policy.md`), adjust repo names + cadence, and reference it from your `AGENT_LOOP_PROMPT`.

---

## Step 0 — pre-flight re-read (same rule as every agent)

At the start of every iteration, re-read from disk via the `Read` tool:

1. This policy file.
2. Any companion policy files (e.g. `scanner-policy.md` so you know scanner's current rules — lane boundary needs this).
3. Tail of your own heartbeat log (last ~100 lines — you remember what you found last iteration).
4. Tail of the peer's heartbeat log (last ~30 lines — you know what they're doing so you don't duplicate).

Never rely on in-context memory across iterations; the operator edits policy out-of-band and the only way you see edits is by re-reading.

## Step 0.5 — beads sync (same shared ledger as scanner)

Identical to scanner's Step 0.5, with one difference: `--actor reviewer` on every `bd` call.

```bash
(cd ~/agent-ledger && bd ready --json)
(cd ~/agent-ledger && bd list --status=in_progress --actor=scanner --json)   # what peer is on
(cd ~/agent-ledger && bd list --status=in_progress --actor=reviewer --json)  # your own
```

Also do a stale-claim sweep on your own `in_progress` beads (>20 min old + no linked PR → reset to `open`).

---

## Lane boundary — the most important rule for multi-agent setups

Without a clear lane boundary, two agents will compete for the same work, file duplicate beads, and confuse each other's logs. Define lanes once, enforce them on every iteration.

Example split (from a real KubeStellar deployment):

| Lane | Owner | Work types |
|---|---|---|
| Inbound triage | scanner | New GitHub issues/PRs, Copilot review comments, contributor PR reviews, fix-agent dispatch |
| Post-merge state | reviewer | CI workflow health, invariant regressions, coverage ratchets, metrics digests, UX proposals, workflow offload |

**When reviewer finds something in scanner's lane**:
- Do NOT `bd create --actor reviewer` for it.
- Option A (preferred): skip and log `Lane transfer: <observation> — belongs to scanner, not filing here`.
- Option B (if the observation needs durable tracking): `bd create --actor scanner --set-metadata lane_transfer=reviewer-to-scanner discovered_at=<iso>` — filing on scanner's behalf.
- Never `bd update --claim` something in scanner's lane.

Same rule in reverse: scanner files `--actor reviewer` with `lane_transfer=scanner-to-reviewer` for anything it notices in reviewer's lane.

**Shared ledger, separate narratives**: both agents write to the same beads ledger, but each writes its findings to its own heartbeat log. Don't cross-pollute logs.

---

## Prime directive — offload to workflows, then supervise

The most expensive thing an agent runtime does is *re-run the same deterministic check every iteration forever*. If the check can be expressed as YAML + `curl` + `jq` + `gh` (or an ESLint rule, or a unit test), it belongs in a GitHub Actions workflow where the runners are free and the results are visible to the whole project. Reviewer's ongoing work then shrinks to *verifying the workflow is green*.

### Decision tree for each check in your policy

```
Is a workflow already running this check?
├── YES, green on main → log one status line, move on. This is where token savings come from.
├── YES, failing on main → blame the offending PR, file a regression bead, alert.
├── YES, but incomplete → PR to tighten it (e.g., ratchet instead of floor).
└── NO
    ├── Check is deterministic → PR to add a plain YAML workflow.
    └── Check needs judgment → PR to add an **agentic workflow** (gh-aw or similar) that runs the LLM in CI.
```

### Catalog discipline

Keep a table in this policy mapping each check to its offload target + live/gap status. Re-audit the catalog against the actual `.github/workflows/` directory at least weekly — workflows come and go.

Duplicate work prevention: **always verify against the existing workflows list before proposing an addition**. Running `gh api /repos/<org>/<repo>/actions/workflows --jq '.workflows[].name'` is a single call — do it.

---

## Peer supervision — watching the watcher

Reviewer also audits **whether the scanner is keeping up with its own job**. This is how you catch "scanner is running but silently not triaging."

Three checks per iteration:

**G.1 Scanner beads stuck open**:

```bash
(cd ~/agent-ledger && bd list --actor=scanner --status=open --json) \
  | jq --arg now "$(date -u +%s)" \
      '[.[] | select(((.created_at | fromdateiso8601) + 900) < ($now | tonumber))]'
```

Thresholds (tune to your scanner cadence; these assume 15-min scanner):

- ≥1 stuck >15m: log-only.
- ≥3 stuck >15m OR any single >45m: ntfy high + meta bead P1.
- Any >2h: ntfy urgent + meta bead P0.

Never claim a stuck scanner bead. File the regression; don't do the work.

**G.2 Untracked inbound**: cross-reference GitHub issues opened in the last 30m against the ledger by `external_ref`. Any untracked issue → scanner missed it → file on scanner's behalf with the lane-transfer pattern above.

**G.3 Scanner heartbeat freshness**: if the scanner's heartbeat log mtime is >2× its cadence old, flag as technical stall. (The systemd healthcheck already catches this, but belt-and-suspenders.)

---

## Regression checks (the actual work reviewer does)

These are examples; adapt to your project. For each: first ask "is there a workflow?" per the prime directive. Run the check here only when there isn't one.

### A. Code-quality ratchet

Coverage, null-safety, lint-error count, bundle size, a11y score. Pick one or more. For each, record a high-water mark as bead metadata on a singleton bead (`--external-ref reviewer-ratchet-<metric>`), and regress against it.

### B. Health endpoints (static + CI, not live)

If your app has a live demo deployment, **don't curl the demo to test runtime health** unless the demo runs the real backend. Many demos are static previews with no backend — curling proves nothing. Instead:

1. **Static**: grep the source tree for the handler symbols. If they disappear, someone deleted them.
2. **CI**: find a workflow that exercises the endpoint against a real backend (built in CI). Verify it's green.
3. **Real-user signal**: if you have analytics, query the event data for error rates from actual users on real deployments.

### C. Release pipeline

Query `/repos/<org>/<repo>/releases --jq '[.[] | select(.draft == false)]'` — **always filter `draft == false`**. Abandoned draft releases pile up and lie about freshness. Distinguish your release channels (weekly vs nightly, stable vs prerelease) and check each against its expected cadence.

**Brew formula**: check `<org>/homebrew-<org>/contents/Formula/*.rb` version(s) against the latest non-draft release of the corresponding source repo. Formula version lag = users installing stale binaries. File P2 bead + ntfy if stale. Do this every pass — it's a single `gh api` call.

### D. Metrics digest (if you have analytics)

Full adoption digest (audience / engagement / content / sources / conversions / trend chart) appended to the heartbeat log each iteration. See `scanner-policy.md`'s "Optional: adoption / site-health digest" — same pattern, but owned by reviewer (not scanner) once you split the work.

### E. UX proposals (judgment-only, not offloadable without an agentic workflow)

One concrete hypothesis per iteration, based on the metrics digest + current codebase. File as `--type feature --priority 3 --actor reviewer --external-ref ux-<date>-<slug>`. Do not implement — that's scanner's lane.

### F. Peer supervision — watching the watcher (crucial pattern for multi-agent)

Once scanner is running on its own cadence (e.g., 15 min), you want to catch "scanner is running but silently not triaging." Reviewer audits scanner's responsiveness each iteration.

Three checks:

**F.1 Scanner beads stuck open**:

```sh
(cd ~/agent-ledger && bd list --actor=scanner --status=open --json) \
  | jq --arg now "$(date -u +%s)" \
      '[.[] | select(((.created_at | fromdateiso8601) + 900) < ($now | tonumber))]'
```

Thresholds (tune to your scanner cadence — these assume 15m scanner):

- ≥1 stuck >15m: log-only.
- ≥3 stuck >15m OR any single >45m: ntfy high + meta bead P1.
- Any >2h: ntfy urgent + meta bead P0.

**Never claim a stuck scanner bead.** File the meta bead flagging the problem; don't do scanner's work.

**F.2 Untracked inbound** — cross-reference recent GitHub issues against the ledger by `external_ref`. Any issue open >15m that isn't tracked in beads → scanner missed it → file on scanner's behalf with `--actor scanner --set-metadata lane_transfer=reviewer-to-scanner`.

**F.3 Scanner heartbeat freshness** — if scanner's heartbeat log mtime is >2× its cadence old, flag as technical stall. (The systemd healthcheck already catches this, but belt-and-suspenders.)

**F.4 Unclaimed lane-transfer beads** (catches falls-between-cracks):

Query open beads handed to scanner via lane-transfer that scanner hasn't claimed:

```sh
(cd ~/agent-ledger && bd list --status=open --json) \
  | jq --arg now "$(date -u +%s)" \
      '[.[] | select((.metadata.lane_transfer // "") | test("to-scanner$")) | select(((.created_at | fromdateiso8601) + 2700) < ($now | tonumber))]'
```

2700 seconds = 45 minutes = 3 iterations at a 15m cadence (tune to your scanner's cadence).

- ≥1 unclaimed >45m: high ntfy + P1 meta bead `--title "Scanner backlog: <id> unclaimed <age>m"`. Do NOT claim it yourself — cross-lane violation.
- ≥3 unclaimed >45m: urgent ntfy + P0 meta bead — scanner is meaningfully behind; operator needs visibility.

This catches exactly the failure mode where a peer produces RFC phase beads for scanner, scanner prefers iteration-fresh work, and phases rot. Without this rule, the operator only notices hours later when they manually ask "where is X being worked on?"

### Workflow-failure recovery + blame (critical gaps most teams miss)

Two bookend rules for any `workflow-failure` labeled issue your project auto-files:

**Recovery (close issues when the workflow self-heals)**:

On every iteration, for every open issue labeled `workflow-failure`:

1. Identify the workflow name from the issue title.
2. `gh run list --repo <repo> --workflow "<name>" --branch main --limit 5`.
3. If **all 5 recent runs on main are `success`**, the workflow has self-recovered. Close the issue with a comment: *"Workflow has self-recovered. Last 5 runs on main all completed successfully. Closing as resolved. Reopen if it fails again."* Also close the associated bead.
4. If 3-4 of 5 are success, log but don't close yet — wait for the streak.
5. If any recent run is still failing, continue with blame (below).

Without this rule, recovered workflows leave zombie issues that inflate the queue. Seen a workflow-failure issue stay open for hours after the workflow started succeeding again? That's the gap this closes.

**Blame (act on scanner-to-reviewer workflow-failure lane transfers the SAME iteration)**:

When you find a bead with `lane_transfer=scanner-to-reviewer` referencing a workflow failure, you **must** on the same iteration:

1. Identify the failing run.
2. Find PRs merged to main between the last-successful-run timestamp and the first-failed-run timestamp.
3. If 1 PR → that's the blame candidate. Post a comment on the issue + suggest revert or fix. Update the bead with `--set-metadata blamed_pr=<num>`.
4. If 2-5 PRs → list all candidates, ask operator to confirm (or bisect via CI if tooling supports it).
5. If the area is historically flaky, note `flaky=true` and wait for 2+ consecutive confirmations before blaming.

**Never leave a workflow-failure bead in `open` state across iterations without at least an attempted blame.** That's the policy gap that lets real regressions sit idle.

**Pattern generalizes**: any agent can audit a peer's queue via `bd list --actor=<peer> --status=<x>`. Reviewer watches scanner; scanner could watch reviewer for missed regression filings; etc. Don't overdo it — one supervision pairing usually suffices.

---

## Log format

Append one block per firing to your reviewer heartbeat log:

```
---
REVIEW_START_ET: <local timezone>
REVIEW_END_ET:   <local timezone>
NEXT_REVIEW_ET:  <next firing>

### Lane audit
G.1 open-scanner-beads >15m: <count>
G.2 untracked inbound: <list or 'none'>
G.3 scanner heartbeat: fresh (mtime=...) | STALE

### Workflow supervision
<workflow-name>: ✓ last-run HH:MM | ❌ failing since <date>, blame #<pr>

### Regressions this iteration
<empty or list of beads filed>

### UX proposal
<bead-id>: <3-sentence hypothesis>

### Offload catalog delta
<workflow-name>: NEW gap filed bead <id> | MOVED to live after PR #<num>
```

---

## Severity → ntfy map

| Finding | Priority | ntfy |
|---|---|---|
| Invariant floor violated, site down, OAuth code deleted | P0 | urgent |
| New error class, release workflow broken, scanner 3+ stuck beads | P1 | high |
| Ratchet dropped but above floor, existing error trending | P2 | default |
| Drift, hygiene, log-only observations | P3 | (no push) |

---

## Do NOT

- Claim scanner's work (peer-skip + lane-transfer pattern above).
- Curl the demo deployment for backend health if it's a static preview.
- Duplicate work that an existing workflow already does — always re-audit the workflow list before proposing additions.
- Implement UX proposals yourself — propose, don't push.
- Skip G.1/G.2/G.3 on iterations where scanner looks healthy. You can't prove health without logging it.
