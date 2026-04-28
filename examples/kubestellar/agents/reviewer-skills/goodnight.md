# Reviewer Skill: Goodnight Docs Sync

Load this when the supervisor sends a "goodnight" work order.

## Goodnight Docs Sync Workflow

When the supervisor sends a "goodnight" work order, run the docs sync workflow:

1. **Version check**: Get latest stable release of `kubestellar/console`:
   ```bash
   unset GITHUB_TOKEN && gh release list --repo kubestellar/console --exclude-pre-releases --limit 1
   ```
   Check if that version exists in `CONSOLE_VERSIONS` in `src/config/versions.ts` on `kubestellar/docs`. If new:
   - Run `node scripts/update-version.js --project console --version <new> --branch docs/console/<new>` (NO `--set-latest`)
   - Open PR with versions.ts + shared.json changes, wait for merge
   - Then create version branch: `git push origin main:docs/console/<new>`

2. **Find last docs sync**: Search for last merged PR on `kubestellar/docs` with label `console-docs-sync` or by author `clubanderson` with "console" in title. Use that merge date as cutoff.

3. **Scan merged PRs**: Get all PRs merged on `kubestellar/console` since the cutoff:
   ```bash
   unset GITHUB_TOKEN && gh pr list --repo kubestellar/console --state merged --limit 200 --search "merged:>YYYY-MM-DD"
   ```

4. **Distill docs-worthy changes**: New features, config options, architecture changes, API changes, user-facing behavior.

5. **Take screenshots** using CDP against **`https://console.kubestellar.io`** logged in as **`demo-user`** (demo mode). **NEVER use localhost. NEVER use clubanderson login. NEVER capture live/real cluster data.** All screenshots must show demo data only.

6. **Create docs PR** on `kubestellar/docs`:
   - Title: `📖 Console docs sync: <date range>`
   - Label: `console-docs-sync`
   - Include screenshots and documentation updates

7. Send ntfy when complete with PR link.
