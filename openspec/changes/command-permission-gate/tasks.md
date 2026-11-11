# Tasks

- [ ] 1. Action input `required-permission` (`write` | `maintain` | `admin`; default `write`), plumbed to dispatch and the pr_comment source
- [ ] 2. Dispatch gate: association allowlist first, then the REST collaborator-permission check, cached per invocation
- [ ] 3. pr_comment approval source applies the same two-stage gate with the refusal-trace style
- [ ] 4. Fail-closed handling: API errors and unknown actors deny below the required level, with the reason
- [ ] 5. Tests: table over permission levels × association allowlist; API failure; unknown actor; dispatch paths unchanged for workflow-owned events
- [ ] 6. Document `required-permission` alongside `allowed-associations`
