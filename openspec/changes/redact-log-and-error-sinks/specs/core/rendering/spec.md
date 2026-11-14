# Rendering - deltas

## MODIFIED Requirements

### Requirement: All rendered output funnels through redaction

Every output sink - log lines in either format, PR comments, timeline events,
notifications, and audit entries - MUST pass its content through the
invocation-scoped redactor. The log handler MUST enforce this at the sink so
no producer can bypass it. A value that fails to reach the sink's redactor -
such as an apply manifest persistence error - MUST be reported to the user in
redacted form at every place it surfaces.

#### Scenario: A registered secret in a log attribute

- **GIVEN** the run registered an auth env value as a secret
- **WHEN** any log record's message or attribute contains that value
- **THEN** the log output shows `[redacted]` instead, in both text and JSON
  formats

#### Scenario: An apply manifest persistence error

- **GIVEN** the apply manifest could not be written
- **WHEN** the persistence error reports credential-shaped values
- **THEN** the job log, timeline event, PR comment, and the returned run
  error all show the redacted form
- **AND** the run still exits nonzero and still reports its stack outcomes

#### Scenario: Every credential-resolving command installs the invocation redactor

- **GIVEN** a command resolves credentials or registers secret values -
  apply, preview, refresh, drift, or any other entry point that resolves an
  auth env
- **WHEN** the command logs anything after the credential resolves
- **THEN** the process log redactor is the command's invocation redactor
  (installed at command entry), so a registered value that matches no
  pattern rule is still masked
- **AND** values the run never registered still pass through - masking is
  registration-driven, not blanket
