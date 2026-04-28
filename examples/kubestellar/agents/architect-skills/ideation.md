# Architect Skill: CNCF Ideation

Load this when proactively generating new feature ideas by scanning the CNCF landscape, or when preparing to open ideation issues for operator approval.

## Ideation — Propose New Features

You proactively generate feature ideas by scanning the CNCF landscape for patterns the console can exploit. The console has low-level integrations with many CNCF projects (Kubernetes, Argo, Kyverno, Istio, etc.) and can derive **high-level correlations** that no single tool can see.

**How to ideate:**
1. Browse CNCF project categories (orchestration, observability, security, networking, runtime, storage, etc.)
2. Look for cross-project correlations — e.g., "Argo deploys + Kyverno policy violations + Istio traffic metrics = deployment risk score"
3. Think about what a human operator would want to see at a glance that currently requires checking 3+ dashboards
4. Open an issue on `kubestellar/console` with:
   - Title: `💡 Feature idea: <short description>`
   - Label: `enhancement`, `architect-idea`
   - Body: problem statement, which CNCF projects are involved, what correlation the console can derive, rough UX sketch
5. **Wait for operator approval** before implementing — once approved, create the fix plan and dispatch to fix agents

**Examples of good correlations:**
- Security posture score (Kyverno violations × OPA audit results × image vulnerability counts)
- Deployment health index (Argo sync status × pod restart rate × Istio error rate)
- Cost efficiency signals (resource requests vs actual usage across clusters)
- Compliance dashboard (CIS benchmarks × policy enforcement × audit log anomalies)

**IMPORTANT**: Ideation issues require operator approval before implementation. Do NOT open a PR based on an ideation issue until the operator explicitly approves it.
