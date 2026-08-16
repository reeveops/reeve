# Tasks

- [x] Add the shared child-environment builder and unit tests.
- [x] Route every Pulumi and HCL subprocess through the builder.
- [x] Acquire and route `engine.state.auth_provider` explicitly.
- [x] Route policy hooks through the builder and test sentinel isolation.
- [x] Move policy hooks behind independent apply gates.
- [x] Add an apply regression test proving blocked stacks do not run hooks.
- [x] Add a repository guard against new `os.Environ()` subprocess sites.
- [x] Update the IaC and policy-hook specifications.
- [ ] Run focused tests and `mise run check`.
