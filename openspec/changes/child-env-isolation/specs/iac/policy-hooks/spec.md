# Policy Hooks - deltas

## ADDED Requirements

### Requirement: Execution boundary

Policy hooks MUST use the constructed child environment and MUST NOT inherit
reeve's full process environment. Apply and state credentials MUST NOT be
acquired before hooks finish.

#### Scenario: Blocked independent gate

- **GIVEN** a stack fails an approval, fork, freeze, lock, preview, check, draft,
  or freshness gate
- **WHEN** reeve evaluates an apply request
- **THEN** no policy hook process starts for that stack

#### Scenario: Independent gates pass

- **GIVEN** every independent gate passes
- **WHEN** policy hooks are configured
- **THEN** reeve runs the hooks against the stored preview
- **AND** a hook evaluation error fails the policy gate closed
