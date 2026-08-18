# Tasks

## Design gate

- [x] Define the command and one-shot record model.
- [x] Define early trust classification and fail-closed behavior.
- [x] Define approved-fork credential selection.
- [x] Define the worker-isolation prerequisite.
- [ ] Approve the OpenSpec design.

## Implementation

- [ ] Add strict command parsing and action dispatch.
- [ ] Add immutable authorization and consumption records.
- [ ] Key the write-once consumption receipt by authorization identity and test concurrent claims under the race detector.
- [ ] Resolve PR metadata before every preview side effect.
- [ ] Fetch, detach, and verify the authorized HEAD in the isolated worker before credential acquisition.
- [ ] Add the approved-fork auth match dimension and fail-closed read-only proof validation.
- [ ] Deny fork apply and refresh without override paths.
- [ ] Add intent, consumption, and terminal audit records.
- [ ] Add malicious fork, stale and mismatched SHA, replay, metadata failure, and audit failure tests.
- [ ] Test unavailable worker isolation, missing approved-fork bindings, invalid read-only proof, and secret-free validation.
- [ ] Test that force and break-glass cannot authorize fork apply or refresh.
- [ ] Add user documentation for runner isolation and read-only IAM.
- [ ] Run `mise run check`, which includes race tests, `govulncheck`, and `gosec`.
- [ ] Run strict OpenSpec validation.
