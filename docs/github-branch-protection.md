# GitHub Branch Protection

ASSOPS keeps the first-version GitHub repository protection as a repository ruleset template in `.github/rulesets/main-required-checks.json`.

The template targets the repository default branch and requires:

- Pull requests before merge.
- One approving review.
- Code owner review according to `.github/CODEOWNERS`.
- Approval from someone other than the last pusher.
- Resolved review threads.
- Fresh required status checks.
- No branch deletion.
- No non-fast-forward pushes.

Required checks:

- `Workflow Lint`
- `Secret Scan`
- `Go`
- `Web`
- `Compose Config`
- `DB Rehearsal`
- `Helm Chart`
- `Helm Smoke`
- `Docker Build (gateway)`
- `Docker Build (worker)`
- `Docker Build (node-worker)`
- `Docker Build (web)`
- `Go Vulnerability Check`

Keep these names aligned with `.github/workflows/ci.yml`. The Docker matrix job names are explicit so GitHub emits stable per-target checks.

## Code Owners

`.github/CODEOWNERS` assigns the first-version default owner and path-specific owners for backend, frontend, deployment, release, and governance files. Keep it current before enabling the ruleset on a shared repository; otherwise pull requests may require review from stale owners.

## Apply

The GitHub REST rulesets API requires repository `Administration: write` permission to create or update a ruleset.

Generate the local no-call application plan first. This validates the checked-in ruleset template and CODEOWNERS coverage, then prints the exact administrator commands without calling GitHub:

```bash
assops-tool release branch-protection-plan \
  <owner>/<repo> \
  .github/rulesets/main-required-checks.json \
  .github/CODEOWNERS \
  .assops/release-notes/branch-protection-plan.md
```

Review the JSON first:

```bash
jq . .github/rulesets/main-required-checks.json
```

Create the ruleset:

```bash
gh api \
  --method POST \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  /repos/<owner>/<repo>/rulesets \
  --input .github/rulesets/main-required-checks.json
```

List active repository rulesets:

```bash
gh api \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  /repos/<owner>/<repo>/rulesets
```

Check which rules apply to the default branch:

```bash
gh api \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  /repos/<owner>/<repo>/rules/branches/<default-branch>
```

## Update

Find the ruleset ID from the list command, then replace the ruleset with the reviewed template:

```bash
gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  /repos/<owner>/<repo>/rulesets/<ruleset-id> \
  --input .github/rulesets/main-required-checks.json
```
