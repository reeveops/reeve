# Preconditions delta

## ADDED Requirements

### Requirement: Preview freshness bounds how stale an applied plan may be

`preconditions.preview_freshness` SHALL default to 4 hours when the key is omitted. Only a literal `"0"` SHALL disable the gate. A value that is not a positive Go duration SHALL be a load error, never a silent disable.

#### Scenario: No configuration

- **WHEN** `preview_freshness` is not set
- **THEN** the gate is active with a 4h window

#### Scenario: Deliberate opt-out

- **WHEN** `preview_freshness: "0"`
- **THEN** the gate is skipped, and its trace says what is no longer checked
