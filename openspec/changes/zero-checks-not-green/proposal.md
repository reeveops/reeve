# zero-checks-not-green

## Why

A commit with no check runs and no combined statuses passed the required-
checks gate: the adapter read the empty evidence as green. A silent CI
outage, a misconfigured repository, or a repo with no checks at all were all
indistinguishable from "everything passed" - the strongest available evidence
was nothing.

## What

Zero check runs AND zero statuses is a denial by default (D2). The refusal
names the SHA, the rule, and the opt-out:

> Required checks: no check runs found on \`\<sha\>\`. Zero checks is not
> treated as passing. Wait for CI, or set \`preconditions.allow_zero_checks\`
> if this repo has no other checks.

The opt-out (`preconditions.allow_zero_checks` in shared.yaml, plus the
matching `--allow-zero-checks` CLI flag on `run apply` and `run explain`) is
for repositories where reeve is the only check. Existing legacy combined-
status checks still count as evidence; zero CHECK RUNS with statuses present
takes the normal status path.

The failing-check detail (and this refusal) becomes the gate's reason on the
PR comment instead of a bare "required checks are not passing".

## Scope

- `internal/vcs/github/reviews.go` (zero-evidence detection + refusal).
- `internal/vcs` (`ChecksGreenOpts.AllowZeroChecks`).
- `internal/core/preconditions` (config surface + failing-detail reason).
- `internal/config/schemas` (`preconditions.allow_zero_checks`).
- The run plumbing (apply/explain inputs) and CLI flags.

## Impact

- **Breaking:** repositories where reeve is the only check are blocked until
  they set `preconditions.allow_zero_checks: true`.
- **Config change:** the new opt-out key; no renames (`reeve migrate-config`
  needs no rule).
