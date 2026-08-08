# `/reeve explain`: the "why?" trace, from the PR

## Why

"Explicit over clever. When a rule fires, you can ask why and get a
trace" is a stated principle. Today it is only half true from GitHub:

- The gate trace arrives with a preview or apply comment. You cannot ask
  for it on demand.
- `reeve rules explain <stack>` and `reeve locks explain <stack>` are
  CLI-only. The engineer staring at a blocked PR is in the browser, not
  a shell with the repo checked out.

The person who most needs the "why" (a contributor whose apply was
blocked) is the person least likely to have local CLI access.

## What

A `/reeve explain [project/stack]` comment command:

- No argument: covers every stack this PR maps to.
- With a stack ref: that stack only.
- Posts one comment per invocation, replace-style per commit (repeat
  invocations at the same commit edit the same comment).

Per stack it renders:

- **Approval rules as resolved**: required count, approver list, group
  requirements, dismiss-on-new-commit. Same data as
  `reeve rules explain`.
- **Lock state**: free, or holder PR, age, and queue position. Same data
  as `reeve locks explain`.
- **Gate evaluation snapshot**: the same per-gate trace the preview
  comment renders, evaluated now, without running the engine.

Strictly read-only: no engine invocation, no lock acquisition, no state
writes, no credential exchange. Dispatchable by anyone who can comment,
like `/reeve help`. Fork PRs included: everything rendered derives from
repo config and reeve's own state bucket, both already visible to anyone
who can read the repo.

## Compatibility

Additive. No existing command, config key, or comment format changes.
Unknown-command handling already posts a help pointer, so old reeve
versions respond to `/reeve explain` with the help text rather than
silence.
