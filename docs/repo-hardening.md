# Repository hardening (#302)

Owner settings that live on GitHub, not in the code. Run these once as the repository owner, in this order. Each step can be undone on its own.

Already in place: Dependabot security updates, grouped version updates (`.github/dependabot.yml`), `.github/CODEOWNERS` without required code-owner review, `environment: release` on the release job.

```bash
R=napfkuchen1/mtmon
```

## 1. Admin bypass on `main` only through a pull request

```bash
ID=$(gh api repos/$R/rulesets -q '.[] | select(.target=="branch") | .id')
gh api repos/$R/rulesets/$ID \
  | jq '.bypass_actors[0].bypass_mode="pull_request" | {name,target,enforcement,conditions,rules,bypass_actors}' \
  | gh api -X PUT repos/$R/rulesets/$ID --input -
```

Check: `gh api repos/$R/rulesets/$ID -q '.bypass_actors'` shows `pull_request`.
Undo: same command with `"always"`.

## 2. Tag ruleset for `v*`

Only admins and the release workflow (GitHub Actions, integration 15368) may create tags. Nobody updates or deletes one without the bypass.

```bash
gh api -X POST repos/$R/rulesets --input - <<'EOF'
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "rules": [ { "type": "creation" }, { "type": "update" }, { "type": "deletion" }, { "type": "non_fast_forward" } ],
  "bypass_actors": [
    { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" },
    { "actor_id": 15368, "actor_type": "Integration", "bypass_mode": "always" }
  ]
}
EOF
```

Check: the next release still creates its tag. Undo: `gh api -X DELETE repos/$R/rulesets/<id>`.

## 3. Environment `release`

Releases start with `workflow_dispatch` on `main`, so the job's ref is the branch. A policy that allows only `v*` tags would block every release: allow `main` too. No required reviewers.

```bash
gh api -X PUT repos/$R/environments/release --input - <<'EOF'
{ "deployment_branch_policy": { "protected_branches": false, "custom_branch_policies": true } }
EOF
gh api -X POST repos/$R/environments/release/deployment-branch-policies -f name=main -f type=branch
gh api -X POST repos/$R/environments/release/deployment-branch-policies -f name='v*' -f type=tag
```

## 4. Move the signing secrets

GitHub never hands out secret values, so they have to be entered again from your own copy (see `docs/release-signing.md`). Commands prompt or read from the file. Never paste the key into an issue or a chat.

```bash
gh secret set MINISIGN_KEY --env release -R $R < /path/to/your/minisign.key
gh secret set MINISIGN_PASSWORD --env release -R $R
```

Then run one release. Only after it has signed successfully, remove the repository-level copies:

```bash
gh secret delete MINISIGN_KEY -R $R
gh secret delete MINISIGN_PASSWORD -R $R
```

## Verifying

- A real release after steps 1–4 shows that the workflow still creates the tag and reads the secrets. Its log shows the job in environment `release`.
- A throwaway tag is not a good dry run: the workflow needs a CHANGELOG section for the tag and would publish a signed release, and published tags are never deleted in this project.
- Refusal check: a `v*` tag push from a token without admin rights must fail with a ruleset error.
