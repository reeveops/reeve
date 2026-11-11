# pulumi-diff-preview-args

## Why

The reviewer-facing diff came from a SECOND `pulumi preview --diff` run that
silently dropped the first run's `--refresh` flag and `ExtraArgs`. Two runs
with different arguments can disagree - a refresh changes the displayed
change set, and operator-supplied extra args change the plan - so the diff the
reviewer read could diverge from the saved plan the apply will execute.

## What

`previewDiff` passes the first run's `--refresh=true` and `ExtraArgs` to the
`--diff` invocation, so both runs use identical plan-affecting arguments. The
clean-no-op skip is unchanged: no second run happens when nothing changes.

## Scope

- `internal/iac/pulumi/pulumi.go` (`previewDiff` argument construction).

## Impact

- No config change.
- The reviewer diff now always matches the first run's plan inputs.
