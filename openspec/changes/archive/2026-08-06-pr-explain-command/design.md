# Design

## Dispatch

- New verb in the comment-command parser next to `preview`/`apply`/etc.
- Optional single argument, validated as `project/stack` against
  discovered stacks. Unknown ref: error comment naming the valid refs.
- Auto-detect from `issue_comment` events like every other verb; CLI
  parity via `reeve run explain [--stack <ref>] --pr <n>`.

## Evaluation

- Reuses the existing gate evaluator in report-only mode: every gate
  runs its check, nothing acts on the result.
- Approval rules come from the same resolution path as
  `reeve rules explain` (general to specific, union, override).
- Lock state is a bucket read. No CAS probe, no acquisition.
- No engine binary is invoked. `preview_fresh` reports against the last
  stored preview, and reports "no preview yet" when there is none.

## Rendering

- One comment per invocation, keyed `explain/<sha>`, edited in place on
  re-invocation at the same commit. Same consolidation rule as the
  timeline comment.
- Per stack: rules block, lock block, gate trace. Gate trace rendering
  is shared with the preview comment, not duplicated.
- All output funnels through `internal/core/redact` like every other
  render path.

## Permissions

- Any commenter can dispatch. The command reveals nothing that repo read
  access does not already grant: approver lists live in `.reeve/`,
  lock holders reference public PR numbers.
- Runs with dry-run credentials on fork PRs; it needs no credentials at
  all, so the fork path and the trusted path are identical.
