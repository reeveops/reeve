# Preconditions

## Evaluation

Ordered, fail-fast. Each gate emits a structured result consumed by the PR
comment renderer. All gates shown in comment regardless of which failed.

1. Branch up-to-date with base.
2. Required checks green.
3. Fresh preview exists for current HEAD SHA (within `preview_freshness`).
4. Preview succeeded (no errors).
5. Policy passed (all blocking policy hooks exit 0).
6. Approvals satisfied for this specific stack.
7. Lock acquirable.
8. Not in freeze window (if configured).

## Break-glass overrides

For an authorized break-glass run (see `openspec/specs/core/approvals`),
gate evaluation overrides the approvals gate unconditionally and the freeze
gate only when `break_glass.override_freeze` is true (the default). An
overridden gate surfaces as a WARNING in the gate trace - visible, never
silent - and is reported in the evaluation result's overridden-gates list
(which feeds the audit record and PR comment). Break-glass NEVER overrides
the lock gate, and leaves every other gate untouched: checks_green,
up_to_date, preview_succeeded, preview_fresh, policy, fork-PR, and draft-PR
all still apply. A gate that would have passed anyway is not reported as
overridden.

## Fork PR gate

If PR is from a fork, apply is denied unless the repo explicitly opts in.
Opt-in documented per-repo; otherwise dry-run-only credentials are used for
preview and apply is refused with a clear message in the PR comment.

## Configuration

Lives in `shared.yaml` `preconditions.*`:
- `require_up_to_date: true`
- `require_checks_passing: true`
- `preview_freshness: 2h`
- `preview_max_commits_behind: 5`
