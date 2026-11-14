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
- Dispatch validates the actor's real repository role through the GitHub
  API the action already authenticates with (`role_name`: `admin`,
  `maintain`, `write`, `triage`, `read`, `none`), and denies anything below
  the required level (`write`, `maintain`, `admin` accepted as
  `required-permission`). Levels below `write` are inputs the mapping still
  reports (`read`, `none`) but always denies; they are rejected as
  `required-permission` values at action-input validation.
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
pr_comment approval source). The `pr_comment` approval source runs INSIDE
the binary, so the bar reaches it through both runtime knobs AGENTS.md
requires:

- CLI flag `--required-permission` (`write` | `maintain` | `admin`),
- config key `approvals.required_permission` (same enum), read from
  TRUSTED configuration.

**Precedence: the CLI flag wins over the config key, and the config key
(trusted configuration) wins over the action input - explicitly set or
default alike.** When the action input is explicitly set AND the config key
is set and they disagree, the config key wins (it is the operator's reviewed
repository authority; the action input rides the caller's workflow file) and
the run's log states which source supplied the effective value. An
unresolvable value at any layer fails closed at load, naming the layer and
the value.

## Compatibility

Breaking: org members (and anyone else) WITHOUT real
`write` access to the repository lose `/reeve` command and comment-approval
ability even when their association passed the allowlist. Repositories that
relied on association-as-proxy keep access through the real-permission check
- any actor who could actually push still passes. The default `write` matches
who can already force the outcome today; `read`/`triage` actors gain nothing
they did not have.
