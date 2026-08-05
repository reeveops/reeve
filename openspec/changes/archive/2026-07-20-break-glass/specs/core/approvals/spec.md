# Approvals — break-glass delta

## ADDED Requirements

### Requirement: Break-glass is an opt-in emergency override of the approvals gate

A `break_glass:` block in shared.yaml SHALL enable an emergency apply path
that overrides the approvals gate in exchange for a mandatory non-empty
justification and a loud immutable audit record. With no block configured,
the break-glass command SHALL fail closed with a polite error explaining
how to enable it, and no run SHALL start.

#### Scenario: Off by default

- **WHEN** `/reeve breakglass "prod down" apply` is invoked on a repo with
  no `break_glass:` block
- **THEN** no run starts and the error explains that break-glass must be
  configured in shared.yaml

#### Scenario: Justification is mandatory

- **WHEN** a break-glass apply is requested with an empty or
  whitespace-only justification
- **THEN** the run is rejected before any lock, credential, or engine call

### Requirement: Authorization is a union of sources, evaluated fail-closed

`break_glass.authorized` SHALL support a union of sources — any source
granting the actor is sufficient: `internal_list` (explicit logins and
`org/team` slugs), `codeowners` (the actor is an owner, directly or via
team, of at least one changed path), and `anyone`. The matched source SHALL
be recorded (narrowest first, so the audit names the most specific grant).
A denial SHALL carry a trace explaining every source consulted. The
`vcs_bypass` source and `groups:` (`group:<provider>:<name>`) SHALL be
accepted by the config parser but rejected at authorization time with clear
"not yet supported" / "phase 2" errors — rejected even when another source
would match, so operators immediately learn the source is inert.

#### Scenario: Union grants via any source

- **WHEN** `authorized:` lists `internal_list: [alice]` and
  `codeowners: true`, and bob (a CODEOWNER of a changed path) invokes
  break-glass
- **THEN** bob is authorized with source `codeowners`

#### Scenario: Denied with trace

- **WHEN** an actor matches no configured source
- **THEN** the run is refused and the failure output traces each source's
  reason (not listed, owns no changed path, ...)

#### Scenario: Unsupported sources are loud

- **WHEN** `vcs_bypass: true` or a `groups:` entry is configured and
  break-glass is invoked
- **THEN** authorization errors with a clear not-yet-supported message and
  nothing is applied

### Requirement: Authorization is head-resolved and self-add is flagged, not forbidden

Authorization SHALL be resolved against the break-glass config and
CODEOWNERS content as of the PR HEAD. Adding oneself to the authorization
surface in the same PR is allowed BY DESIGN (the emergency responder may
need to). The audit record SHALL flag when any authorizing file — a
`.reeve/*.yaml`/`.yml` config or a CODEOWNERS file (`CODEOWNERS`,
`.github/CODEOWNERS`, `docs/CODEOWNERS`) — was modified in the same PR,
listing the touched paths.

#### Scenario: Self-add works but is flagged

- **WHEN** a PR both adds mallory to `internal_list` and mallory invokes
  break-glass on that PR
- **THEN** the apply may proceed, and the audit record and PR comment carry
  the authorizing-config-modified flag with `.reeve/shared.yaml` listed
