# PR flow delta

## ADDED Requirements

### Requirement: Preview exit status reflects stack outcomes

Preview MUST exit nonzero when any stack it targeted failed to plan.

Preview MUST still post its PR comment and write its artifacts before
returning that status, so the failure detail reaches reviewers.

Preview MUST log each stack failure with the stack reference and the redacted
error, at a level that a default log configuration surfaces.

#### Scenario: A stack fails to plan

- **WHEN** one or more targeted stacks return an error during preview
- **THEN** the PR comment and run artifacts are written
- **AND** each failure is logged with its stack reference
- **AND** the command exits nonzero

#### Scenario: Every stack plans

- **WHEN** every targeted stack plans without error
- **THEN** the command exits success

### Requirement: Preview reports comment delivery honestly

Preview MUST report a posted PR comment only when a comment was written.

#### Scenario: No comment surface configured

- **WHEN** preview runs without a comment client or PR number
- **THEN** the run does not claim to have posted a comment
