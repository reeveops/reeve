# Preconditions

## Evaluation

Apply uses fail-fast evaluation; report-only explain evaluates all gates for a full trace.
Independent fork, draft, branch, checks, preview, approvals, lock, and freeze checks precede repository-controlled policy execution.

Policy runs only for a stack that passes those independent gates, and remains the final fail-closed apply gate.
Apply credentials and backend login are resolved after policy passes.

## Break-glass overrides

For an authorized break-glass run (see `openspec/specs/core/approvals`),
gate evaluation overrides the approvals gate unconditionally and the freeze
gate only when `break_glass.override_freeze` is true (the default). An
overridden gate surfaces as a WARNING in the gate trace and is reported in
the evaluation result's overridden-gates list. If selected preview history
cannot be read or decoded, an authorized break-glass run MAY also override
`preview_succeeded` and `preview_fresh` so the repository can recover.
Break-glass NEVER overrides the lock, checks, up-to-date, policy, fork-PR, or
draft-PR gates. An ordinary missing, stale, or failed preview remains binding.
A gate that would have passed anyway is not reported as overridden.

## Fork PR gate

If PR is from a fork, apply is denied unless the repo explicitly opts in.
Preview credentials come from configured preview bindings and workflow credential availability; this gate does not reduce their IAM permissions.
A denied apply is reported in the PR comment.

## Configuration

Lives in `shared.yaml` `preconditions.*`:
- `require_up_to_date: true`
- `require_checks_passing: true`
- `preview_freshness: 2h`
- `preview_max_commits_behind: 5`
