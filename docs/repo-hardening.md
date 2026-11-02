# Repository hardening (maintainer checklist)

These settings live in GitHub, not in the code, so a pull request cannot change them. Everything the code can do
is already in the repository: workflows pinned by commit SHA with minimal per-job `permissions`, SBOM attestations
in the release, grouped Dependabot updates and `.github/CODEOWNERS`.

| # | Setting | Where | Target |
|---|---------|-------|--------|
| 1 | Admin bypass on `main` | Settings → Rules → Rulesets → "main schützen" → Bypass list | Role *Admin*, mode **For pull requests only** (not *Always*) |
| 2 | Tag protection | Rulesets → New tag ruleset, target `refs/tags/v*` | Restrict creations, updates and deletions; bypass only for admins |
| 3 | Signing key behind an environment | Settings → Environments → New `release` | Deployment branch `main`; move `MINISIGN_KEY` and `MINISIGN_PASSWORD` from repository secrets into it; then add `environment: release` to the `release` job in `release.yml` (do both in the same change, otherwise the release has no key) |
| 4 | Dependabot security updates | Settings → Advanced Security | On (version updates are already grouped per ecosystem) |
| 5 | Code-owner review | Ruleset "main schützen" → *Require review from Code Owners* | On; with 0 required approvals only owner-listed paths wait for the owner |

Check afterwards: push a test branch and try `git push origin HEAD:main` (must be refused), and create a `v0.0.0-test`
tag from a non-admin token (must be refused).
