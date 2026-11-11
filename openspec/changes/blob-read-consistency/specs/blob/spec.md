# Blob store - deltas

## ADDED Requirements

### Requirement: Read body and version coherence

A read MUST return the object body and version that were published together.
A store MUST NOT return a body from one write paired with a version from
another. This is the read half of the If-Match guarantee: a caller that
re-reads after a failed conditional write sees the winner, never a stale
hybrid.

#### Scenario: A read overlapping a replacement

- **WHEN** a reader reads a key while a writer replaces it
- **THEN** the body and version the reader receives come from the same write
- **AND** a conditional write using that version compares against the
  replacement, not against a pre-replacement state
