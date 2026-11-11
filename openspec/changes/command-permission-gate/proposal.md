# command-permission-gate

## Why

`/reeve` command dispatch and comment approvals gate on `author_association`
(`OWNER`, `MEMBER`, `COLLABORATOR`). `MEMBER` means org membership, not
access: an org with thousands of members grants none of them repo access, and
`COLLABORATOR` is a stale-by-design concept GitHub no longer surfaces in the
web UI. Who can run `/reeve apply` (a write against the operator's cloud
account) is currently decided by a proxy for permission rather than by
permission.

## What

- A new action input, `required-permission` (default `write`), states the
  minimum repository permission needed to dispatch `reeve` commands and to
  have a `pr_comment` approval counted.
- Dispatch validates the actor's real repository permission through the
  GitHub API the action already authenticates with, and denies anything
  below the required level (`write`, `maintain`, `admin` accepted; reading
  lower levels down to `write`).
- `allowed-associations` stays: association remains the cheap prefilter and
  the coarse deny-list (`NONE` is denied by default). An actor whose
  association passes the allowlist but whose real permission is below
  `required-permission` is denied with a reason naming both facts.
- The pr_comment approval source enforces the same permission bar for its
  commenters, using one permission lookup per comment author per
  invocation.
- The explicit `command` input dispatch path (trusted workflow-owned events:
  `workflow_dispatch`, `schedule`, `push`) is unchanged: it has no comment
  actor to authorize - the caller is the workflow, not a user.

## Scope

Action input + the two authorization surfaces (dispatch classification,
pr_comment approval source). No reeve config schema changes: the
`required-permission` knob is Action-level, matching where
`allowed-associations` already lives.

## Compatibility

Breaking for the plan's D3: org members (and anyone else) WITHOUT real
`write` access to the repository lose `/reeve` command and comment-approval
ability even when their association passed the allowlist. Repositories that
relied on association-as-proxy keep access through the real-permission check
- any actor who could actually push still passes. The default `write` matches
who can already force the outcome today; `read`/`triage` actors gain nothing
they did not have.
