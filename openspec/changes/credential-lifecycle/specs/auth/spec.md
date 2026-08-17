# Auth delta

## ADDED Requirements

### Requirement: Failed credential acquisition unwinds prior providers

The registry MUST clean every credential acquired for a request when a later provider fails.
Cleanup MUST run in reverse acquisition order and MUST preserve cleanup failures with the acquisition error.

#### Scenario: A later provider fails

- **WHEN** two providers return credentials and a third provider fails
- **THEN** the second and first credentials are cleaned in that order
- **AND** no partial environment or credential list is returned

#### Scenario: Rollback cleanup fails

- **WHEN** credential acquisition fails and a prior credential cleanup also fails
- **THEN** the returned error identifies both failures
