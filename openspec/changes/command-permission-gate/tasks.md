# Tasks

- [ ] 1. Action input `required-permission` (`write` | `maintain` | `admin`; default `write`), plumbed to dispatch and the pr_comment source
- [ ] 2. Dispatch gate: association allowlist first, then the REST collaborator-permission check, cached per invocation
- [ ] 3. pr_comment approval source applies the same two-stage gate with the refusal-trace style
- [ ] 3b. The mapping reads the response's `user.permissions` flags (one-to-one: `admin` -> admin, `maintain` -> maintain, `push` -> write; triage/pull-only below the bar). A custom repository role maps through its INHERITED base role - the hash carries base-role permissions, so `role_name` is never the level source. A response that cannot be interpreted (missing `user.permissions`, null collaborator, unparseable object) fails closed, naming exactly what it saw
- [ ] 3d. Verify the flag mapping against the REAL API on a repository with a custom repository role (observe the hash and `role_name` for a custom-role holder) BEFORE this ships; if reality disagrees with the documented base-role reading, the mapping reverts to fail-closed and this spec changes first
- [ ] 3c. Runtime knobs per AGENTS.md: CLI flag `--required-permission` (wins) and config key `approvals.required_permission` read from trusted configuration; flag > config > the action input's default; bad values fail closed at load naming layer and value
- [ ] 4. Fail-closed handling: API errors and unknown actors deny below the required level, with the reason
- [ ] 5. Tests: table over permission levels × association allowlist; API failure; unknown actor; dispatch paths unchanged for workflow-owned events
- [ ] 6. Document `required-permission` alongside `allowed-associations`
