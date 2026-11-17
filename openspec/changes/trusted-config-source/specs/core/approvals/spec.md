# Approvals (break-glass) - deltas

## REMOVED Requirements

### Head-resolved; self-add flagged, not forbidden (with opt-in lockdown)

**Reason**: The single head-resolved rule is replaced by trusted-resolution of the `break_glass:` block plus an independent, optional CODEOWNERS revision lock.

**Migration**: Existing head-resolved CODEOWNERS behavior remains the default. Set `break_glass.codeowners_from_base: true` in trusted configuration to make PR CODEOWNERS edits inert for break-glass until merge. `break_glass.reject_self_authorization` keeps its existing independent behavior.

## ADDED Requirements

### Requirement: Break-glass CODEOWNERS base locking is optional and off by default

The `break_glass:` block MUST resolve from `trusted_config_revision`. `break_glass.codeowners_from_base` MUST default to `false`: false preserves HEAD-resolved CODEOWNERS, while true resolves the break-glass `codeowners` source from the exact trusted base SHA. When true, PR CODEOWNERS edits MUST be inert until merge. Base-authorized reviewers and operators MUST remain able to approve under ordinary rules and invoke Reeve on a PR changing both infrastructure and CODEOWNERS, subject to the independent `reject_self_authorization` rule.

#### Scenario: Default preserves head-resolved CODEOWNERS

- **GIVEN** `break_glass.codeowners_from_base` is omitted or false
- **WHEN** the break-glass `codeowners` source evaluates a PR
- **THEN** it resolves CODEOWNERS from PR HEAD exactly as before
- **AND** the audit still records any authorizing-file modification

#### Scenario: A PR grants itself a CODEOWNERS path with locking enabled

- **GIVEN** `break_glass.codeowners_from_base: true` in trusted configuration
- **AND** a PR whose HEAD adds a CODEOWNERS entry making the author an owner of a changed path
- **WHEN** the PR runs `/reeve breakglass` relying on the `codeowners` source
- **THEN** CODEOWNERS is read from the trusted base SHA, where the entry is absent
- **AND** the `codeowners` source does not grant, and the apply is denied unless another trusted source grants

#### Scenario: Base-authorized operator handles a mixed infrastructure and CODEOWNERS PR

- **GIVEN** `break_glass.codeowners_from_base: true`
- **AND** `break_glass.reject_self_authorization` is false
- **AND** a PR changes both infrastructure and CODEOWNERS
- **AND** the operator is authorized by the base revision
- **WHEN** the operator invokes `/reeve breakglass`
- **THEN** Reeve uses the base branch as the CODEOWNERS ref and the HEAD CODEOWNERS edit is inert until merge
- **AND** the operator remains eligible to invoke Reeve without authority from the HEAD edit
- **AND** reviewers authorized on the base remain eligible to approve under the ordinary approval rules

### Requirement: Authorizing-file handling covers both config extensions

The authorizing-file set MUST include `.reeve/*.yaml`, `.reeve/*.yml`, `CODEOWNERS`, `.github/CODEOWNERS`, and `docs/CODEOWNERS`. Every matching modification MUST produce the `authorizing-config-modified` audit signal with touched paths. `break_glass.reject_self_authorization` MUST retain its independent default `false`; when true, any such modification denies break-glass before source evaluation.

#### Scenario: reject_self_authorization distrusts either YAML extension

- **GIVEN** `break_glass.reject_self_authorization: true`
- **AND** an actor listed in the trusted revision's `break_glass.authorized`
- **WHEN** that actor runs `/reeve breakglass` from a PR that modified a `.reeve/*.yaml` or `.reeve/*.yml` config or a recognized CODEOWNERS file
- **THEN** the apply is denied before any source is evaluated
- **AND** the trace names the touched paths

#### Scenario: The modification flag records attempts in either CODEOWNERS mode

- **GIVEN** a PR that modifies a `.reeve/*.yaml`, `.reeve/*.yml`, or recognized CODEOWNERS file
- **WHEN** a break-glass apply runs on that PR with base locking either enabled or disabled
- **THEN** the audit record flags `authorizing-config-modified` with the touched paths
- **AND** trusted configuration edits are inert for the current run
- **AND** CODEOWNERS edits are inert only when `break_glass.codeowners_from_base` is true

### Requirement: Ordinary approvals intentionally remain HEAD-resolved

Ordinary approval CODEOWNERS integration MUST continue resolving from PR HEAD, regardless of `break_glass.codeowners_from_base`. The optional lock is scoped to emergency authorization so existing review ownership and routing remain stable. Non-author, freshness, dismissal, and required-approval rules MUST remain unchanged.

#### Scenario: Base locking for break-glass does not move ordinary approvals

- **GIVEN** `break_glass.codeowners_from_base: true`
- **WHEN** ordinary approvals resolve CODEOWNERS for the same PR
- **THEN** ordinary approvals use PR HEAD
- **AND** only the break-glass `codeowners` source uses the trusted base SHA
