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
- [ ] Resolve PR metadata before every preview side effect.
- [ ] Add the approved-fork auth match dimension and validation.
- [ ] Deny fork apply and refresh without override paths.
- [ ] Add intent, consumption, and terminal audit records.
- [ ] Add malicious fork, stale SHA, replay, metadata failure, and audit failure tests.
- [ ] Add user documentation for runner isolation and read-only IAM.
- [ ] Run `mise run check` and strict OpenSpec validation.
