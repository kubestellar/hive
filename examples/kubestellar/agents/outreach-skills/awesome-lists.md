# Outreach Skill: Awesome Lists and Directories

Load this when working on Mission B: submitting KubeStellar Console to awesome lists, directories, and community sites.

## Standing Mission B: Organic Search & Traffic (PRIMARY FOCUS)

Goal: Maximize KubeStellar Console's presence across every discoverable surface.

**Target: 200 awesome lists and directories.** Find 200 GitHub awesome-* lists, directories, aggregators, and curated collections where KubeStellar Console can be listed. Open PRs or issues for each one. Each pass should add 10-20 new targets.

**Channels to target (non-exhaustive — find more):**
1. **Awesome lists** — Any GitHub awesome-* list touching Kubernetes, cloud-native, dashboards, monitoring, SRE, DevOps, AI/ML ops, security, Docker, CNCF, multi-cluster, edge computing
2. **Directories & registries** — CNCF Landscape, Artifact Hub, OperatorHub, Helm Hub, CNCF project pages
3. **Comparison sites & blogs** — Kubernetes dashboard comparisons, "best K8s tools" roundups, tech blog aggregators
4. **Community forums** — Reddit (r/kubernetes, r/devops), Hacker News Show HN, dev.to, Hashnode, Medium publications
5. **Social bookmarking** — Product Hunt, AlternativeTo, StackShare, G2, TechStacks
6. **Conference/meetup CFPs** — KubeCon, CNCF meetups, cloud-native webinars

**When adding to lists, use this description:**
> KubeStellar Console — Multi-cluster Kubernetes dashboard with AI-powered operations, CNCF project integrations, and real-time observability across edge and cloud clusters.

**Key differentiators to highlight:**
- Multi-cluster management (not just single cluster)
- AI/LLM integration for operations
- CNCF Sandbox project
- Integrates with Argo, Kyverno, Istio, and 20+ CNCF projects
- GitHub OAuth, benchmark tracking, compliance dashboards
- Enterprise features: supply chain security, SBOM, SLSA, identity management
- Real-time streaming benchmarks and hardware leaderboards

## PR Follow-up — CRITICAL

After opening PRs on external repos, monitor them for review comments and address feedback promptly.

**On every pass:**
1. List all open PRs by `${PROJECT_AI_AUTHOR}` across your target repos:
   ```bash
   unset GITHUB_TOKEN && gh search prs --author ${PROJECT_AI_AUTHOR} --state open --limit 100 --json repository,number,title,updatedAt
   ```
2. Also check **closed** PRs for maintainer feedback inviting resubmission:
   ```bash
   unset GITHUB_TOKEN && gh search prs --author ${PROJECT_AI_AUTHOR} --state closed --limit 100 --json repository,number,title,comments
   ```
3. For each open PR updated recently, check for review comments:
   ```bash
   unset GITHUB_TOKEN && gh pr view <N> --repo <owner/repo> --json reviews,comments,state
   ```
4. If there are unaddressed comments from CodeRabbit, Copilot, or human reviewers:
   - Read the feedback carefully
   - Make the requested changes in a new commit on the same branch
   - Push the update
   - Reply to the review comment acknowledging the fix
5. Send ntfy for every PR update.

**Do NOT ignore review comments.** Unresponsive PRs get closed. Address feedback within the same pass you discover it.

## Known Prolific Maintainers — Watch Carefully

### brandonhimpfen (maintains 20+ awesome-* repos)

**His feedback pattern:**
- Always responds with a comment before closing
- Either: *"not a fit for this list"* (COLD — do not resubmit) or *"please resubmit under [X] section"* (ACTION — resubmit with corrected section + neutral description)
- **Always** requests neutral language: no "AI-powered", no feature bullet lists
- Uses em dash `–` (not hyphen `-`) as separator in list entries — match exactly

**Resubmission rules for brandonhimpfen repos:**
1. Check closed PRs first: `gh pr list -R brandonhimpfen/<repo> --author ${PROJECT_AI_AUTHOR} --state closed --json number,comments`
2. If feedback says "resubmit under X": fork the *brandonhimpfen* repo directly, add to the specified section, neutral description, open new PR referencing the old one
3. If feedback says "not a fit": mark COLD, never retry that specific repo
4. Always fork with `gh repo fork brandonhimpfen/<repo>` and verify `gh api repos/${PROJECT_AI_AUTHOR}/<fork> --jq '.parent.full_name'` returns `brandonhimpfen/<repo>` before pushing

**Status as of 2026-04-24:**
| Repo | Status |
|------|--------|
| `awesome-ai-infrastructure` | PR #11 open (resubmit to Cloud Platforms) |
| `awesome-devops` | PR #2 open (resubmit to DevOps Platforms) |
| `awesome-ai-edge-computing` | COLD — "not a fit" |
| `awesome-mlops` | COLD — "not a fit" |
| `awesome-kubernetes` | COLD — duplicate closed |

## Current Progress (as of 2026-04-24)

### Mission B — Awesome Lists PRs Opened (257+ external open PRs, 16 cold/closed repos)

**Key open PRs (sample — full list via `gh search prs --author ${PROJECT_AI_AUTHOR} --state open --limit 100`):**
- `4ndersonLin/awesome-cloud-security` — open PR
- `51nk0r5w1m/awesome-cloudNative` — open PR
- `adriannovegil/awesome-observability` — PR #53
- `adriannovegil/awesome-sre` — open PR
- `agamm/awesome-ai-sre` — open PR
- `akuity/awesome-argo` — open PR
- `awesome-mlops/awesome-mlops-kubernetes` — open PR
- `awesome-mlops/awesome-mlops-platforms` — open PR
- `awesome-sre/awesome-sre` — open PR
- `bijualbert/awesome-cloud-native-trainings` — open PR
- `bregman-arie/devops-resources` — open PR
- `cdwv/awesome-helm` — open PR
- `CognonicLabs/awesome-AI-kubernetes` — open PR
- `crazy-canux/awesome-monitoring` — PR #38
- `Curated-Awesome-Lists/awesome-devops-tools` — open PR
- `dastergon/awesome-chaos-engineering` — open PR
- `dastergon/awesome-sre` — PR #268
- `datreeio/awesome-gitops` — open PR
- `devsecops/awesome-devsecops` — open PR
- `fititnt/awesome-aiops` — open PR
- `gorkemozlu/awesome-sre-tools` — open PR
- `hysnsec/awesome-policy-as-code` — open PR
- `iGusev/awesome-cloud-native` — open PR
- `infracloudio/awesome-platform-engineering` — open PR
- `InftyAI/Awesome-LLMOps` — open PR
- `JakobTheDev/awesome-devsecops` — open PR
- `jatrost/awesome-kubernetes-threat-detection` — open PR
- `jmfontaine/awesome-finops` — open PR
- `joubertredrat/awesome-devops` — open PR
- `kelvins/awesome-mlops` — open PR
- `ksoclabs/awesome-kubernetes-security` — open PR
- `kubernetes-hybrid-cloud/awesome-kubernetes` — open PR
- `last9/awesome-sre-agents` — PR #12
- `LeanerCloud/awesome-finops` — open PR
- `Lets-DevOps/awesome-learning` — open PR
- `lukecav/awesome-kubernetes` — open PR
- `magnologan/awesome-k8s-security` — open PR
- `mahomedalid/awesome-multicloud` — PR #2
- `mahseema/awesome-ai-tools` — PR #1184
- `magsther/awesome-opentelemetry` — open PR
- `mehdihadeli/awesome-software-architecture` — open PR
- `Metarget/awesome-cloud-native-security` — PR #13
- `mfornos/awesome-microservices` — open PR
- `mikeroyal/Kubernetes-Guide` — open PR
- `mstrYoda/awesome-istio` — open PR
- `myugan/awesome-cicd-security` — open PR
- `nataz77/awesome-k8s` — open PR
- `ndrpnt/awesome-kubernetes-configuration-management` — open PR
- `nik-kale/awesome-autonomous-ops` — open PR
- `nirgeier/awesome-devops` — open PR
- `NotHarshhaa/awesome-devops-cloud` — open PR
- `NotHarshhaa/devops-tools` — open PR
- `nubenetes/awesome-kubernetes` — open PR
- `obazoud/awesome-dashboard` — PR #35
- `omarkdev/awesome-dashboards` — PR #31 (COLD — closed)
- `pavangudiwada/awesome-ai-sre` — PR #21
- `paulveillard/cybersecurity-devsecops` — open PR
- `pditommaso/awesome-k8s` — open PR
- `pditommaso/awesome-pipeline` — open PR
- `ramitsurana/awesome-kubernetes` — PR #1088
- `roaldnefs/awesome-prometheus` — open PR
- `rohitg00/kubernetes-resources` — open PR
- `run-x/awesome-kubernetes` — open PR
- `sbilly/awesome-security` — open PR
- `seifrajhi/awesome-platform-engineering-tools` — PR #147
- `ShakedBraimok/awesome-platform-engineering` — open PR
- `shibuiwilliam/awesome-k8s-crd` — open PR
- `siderolabs/awesome-talos` — open PR
- `SigNoz/Awesome-OpenTelemetry` — open PR
- `sottlmarek/DevSecOps` — PR #99
- `SquadcastHub/awesome-sre-tools` — open PR
- `techiescamp/devops-tools` — open PR
- `tensorchord/Awesome-LLMOps` — open PR
- `thedevopstooling/awesome-kubernetes-for-devops` — open PR
- `tmrts/awesome-kubernetes` — open PR (COLD — archived)
- `tomhuang12/awesome-k8s-resources` — PR #187
- `toptechevangelist/awesome-platform-engineering` — open PR
- `trimstray/the-book-of-secret-knowledge` — open PR
- `tuan-devops/awesome-kubernetes` — open PR
- `tysoncung/awesome-devops-platform` — PR #1
- `tysoncung/awesome-devsecops` — open PR
- `veggiemonk/awesome-docker` — PR #1417
- `vilaca/awesome-k8s-tools` — open PR
- `visenger/awesome-mlops` — open PR
- `warpnet/awesome-prometheus` — open PR
- `weaveworks/awesome-gitops` — PR #70
- `wh211212/awesome-cloudnative` — open PR
- `wmariuss/awesome-devops` — open PR
- `yarncraft/awesome-edge` — open PR

**Cold/closed repos (never retry):**
- brandonhimpfen/* (5 repos) — closed PRs
- open-policy-agent/awesome-opa — closed
- ripienaar/free-for-dev — closed
- servicemesher/awesome-servicemesh — closed
- cloudnativebasel/awesome-cloud-native — closed
- awesome-foss/awesome-sysadmin — closed
- rezmoss/awesome-security-pipeline — closed
- qijianpeng/awesome-edge-computing — already listed
- shospodarets/awesome-platform-engineering — already listed
