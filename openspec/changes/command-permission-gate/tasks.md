# Tasks

- [ ] 1. Action input `required-permission` (`write` | `maintain` | `admin`; default `write`), plumbed to dispatch and the pr_comment source
- [ ] 2. Dispatch gate: association allowlist first, then the REST collaborator-permission check, cached per invocation
- [ ] 3. pr_comment approval source applies the same two-stage gate with the refusal-trace style
- [ ] 3b. The mapping reads the response's `role_name` field (GitHub maps maintain to `write` in legacy `permission`); an unrecognized `role_name` (custom repository role) fails closed, naming the role
- [ ] 3c. Runtime knobs per AGENTS.md: CLI flag `--required-permission` (wins) and config key `approvals.required_permission` read from trusted configuration; flag > config > the action input's default; bad values fail closed at load naming layer and value
- [ ] 4. Fail-closed handling: API errors and unknown actors deny below the required level, with the reason
- [ ] 5. Tests: table over permission levels × association allowlist; API failure; unknown actor; dispatch paths unchanged for workflow-owned events
- [ ] 6. Document `required-permission` alongside `allowed-associations`
