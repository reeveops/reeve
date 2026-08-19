# IaC delta

## ADDED Requirements

### Requirement: Engine failures report the whole cause

An adapter MUST NOT truncate an engine failure message. The reported error
MUST carry every line the engine produced for that failure.

When an engine reports failures as structured diagnostics, the adapter MUST
report those diagnostics. Diagnostics MUST take precedence over the process
exit error, which names no resource. Stderr MUST be appended rather than
dropped whenever it is non-empty, regardless of exit code, since an engine can
report diagnostics while exiting zero and stderr can carry a backend or
authentication failure absent from the diagnostic stream.

An adapter MUST report an error whose severity the engine marks as an error,
and MUST NOT promote an informational or warning diagnostic to an error.

Parsed change counts and reported errors are independent: a partially applied
update MUST report both.

#### Scenario: Engine reports diagnostics on stdout

- **WHEN** an apply fails and the engine emits error diagnostics on its event stream
- **THEN** the reported error carries every error diagnostic
- **AND** informational and warning diagnostics are not reported as errors

#### Scenario: Diagnostics reported with a zero exit

- **WHEN** an engine reports error diagnostics and exits zero with stderr output
- **THEN** the reported error carries both the diagnostics and the stderr text

#### Scenario: Process fails with no diagnostics

- **WHEN** an apply fails leaving only stderr and an exit error
- **THEN** the reported error carries the stderr text and names the process error

#### Scenario: Failure with no output at all

- **WHEN** a command fails producing neither diagnostics nor stderr
- **THEN** the reported error names the process failure rather than being empty
