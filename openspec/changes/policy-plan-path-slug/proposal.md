# policy-plan-path-slug

## Why

The policy hook plan file was written to `filepath.Join(tmpdir, "<project>-<stack>.json")` with the project and stack names composed verbatim. Both come from the repo under review, and `filepath.Join` cleans `..` - so a crafted stack name wrote the plan JSON (engine plan bodies, counts) to any path the CI runner could write.

## What

The plan filename is built from slugged components (`blob.SlugComponent`), so any name - traversal, slashes, degenerate - stays inside the per-run temp directory. Ordinary names keep their existing `<project>-<stack>.json` filename.

A plan file that cannot be written now refuses with the reason the hook runner reports to the job log, instead of a bare wrapped error:

> policy hooks \<names\> not run for \<stack\>: plan file could not be written (\<error\>); apply denied

No hook runs; the policy gate fails closed; the temp dir is not left behind.

## Scope

- `internal/run/policy_helper.go` only.

## Impact

- No config change; no user-visible filename change for ordinary names.
