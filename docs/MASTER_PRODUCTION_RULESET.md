# Master production ruleset

Use this ruleset to protect the `master` branch.

## Ruleset

- Name: `master-production`
- Target branch: `master`
- Enforcement: active

## Required rules

- Require pull request before merge.
- Require at least one approval.
- Dismiss stale approvals when new commits are pushed.
- Require all conversations to be resolved before merge.
- Require status checks before merge.
- Require the branch to be up to date before merge.
- Block force pushes.
- Block branch deletion.

## Required checks

- `go-test`
- `go-vet`
- `golangci-lint`

## Recommended later

- Require signed commits after the team is ready.
- Add CodeQL once the project has stable CI.
- Allow bypass only for repository admins.

## Why this matters

The `master` branch is the production branch. It should not accept direct changes.

Every production change should go through a pull request, pass checks, and be reviewed before merge.
