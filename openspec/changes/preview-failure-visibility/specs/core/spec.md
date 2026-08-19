# Core delta

## ADDED Requirements

### Requirement: Run outcome is observable in logs and exit status

A run whose work failed MUST NOT report success.

Every failure that changes a run's outcome MUST be logged with the identifier
of the unit that failed, in addition to any rendered surface it appears on.

Redaction applies to logged failure text exactly as it applies to rendered
output: logged errors MUST pass through the configured redactor.

#### Scenario: Unit of work fails

- **WHEN** a stack, gate, or terminal write fails during a run
- **THEN** the failure is logged with its identifier and redacted error
- **AND** the run exits nonzero

#### Scenario: Setup blocks on the network

- **WHEN** blob store open, engine construction, or credential exchange runs
- **THEN** each step reports its duration so a slow run is attributable
