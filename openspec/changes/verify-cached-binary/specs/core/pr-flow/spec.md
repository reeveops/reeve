# PR flow - deltas

## ADDED Requirements

### Requirement: Cached binaries are verified before use

A restored binary cache entry MUST be verified against a hash recorded when
it was saved before anything executes it. The cache key binds the entry to
the action source; the verification binds the FILE to the bytes that were
hashed. A mismatched or incomplete entry MUST be deleted and the run MUST
rebuild from source, with a visible line naming the recorded hash, the actual
hash, and the outcome. No cached binary is executed without passing
verification.

#### Scenario: A tampered cache entry

- **GIVEN** a restored cache entry whose binary does not match its recorded
  hash
- **WHEN** the verify step runs
- **THEN** the entry is deleted
- **AND** the run reports the sidecar hash, the actual hash, and that it
  rebuilds from source
- **AND** the build path steps run because verification failed
