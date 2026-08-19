# Tasks

## Design gate

- [ ] Confirm `private-modules-token` is an acceptable operator-chosen widening
      given the `child-env-isolation` security boundary.

## Implementation

- [x] Add the private-module, cache, prewarm, and Terraform-pin inputs.
- [x] Configure the git URL rewrite for both URL forms and export `GOPRIVATE`.
- [x] Warn on every run that uses `private-modules-token`.
- [x] Warn when `private-modules-token` is set without `go-private`.
- [x] Export the runner's resolved `GOMODCACHE` and `GOCACHE`.
- [x] Fail with a clear message when `go-cache` is set without Go on PATH.
- [x] Build the program before reeve runs when `prewarm-dir` is set.
- [x] Resolve Pulumi plugin versions from `go.mod` and fail when absent.
- [x] Read `engine.binary.version` and install Terraform pinned to it.
- [x] Fail when the version or the config file is missing.

## Verification

- [x] Terraform version parser extracts the value and ignores the schema's
      top-level `version:`.
- [x] Parser step fails with a clear message when the field is absent.
- [x] `setup-terraform` SHA pin matches the upstream `v3` tag.
- [x] `action.yml` and both example workflows parse.
- [ ] End-to-end run on a repository with private modules.

## Documentation

- [x] Getting-started section covering the isolation consequences and inputs.
- [x] `examples/private-modules/` with PR and drift workflows plus a README.
- [x] Example index entry.
