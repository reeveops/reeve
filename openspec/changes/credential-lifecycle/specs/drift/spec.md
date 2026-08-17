# Drift delta

## ADDED Requirements

### Requirement: Drift owns acquired credential cleanup

Each drift stack check MUST clean its acquired credentials when the check finishes.
An auth-expiry rebind MUST clean the expired credential set before acquiring its replacement.

#### Scenario: A drift check completes

- **WHEN** a drift check acquires a credential set and reaches an outcome without rebind
- **THEN** that credential set's cleanup callback runs exactly once

#### Scenario: Expired credentials are rebound

- **WHEN** a drift check retries after an auth-expired result
- **THEN** the expired credential cleanup runs before replacement acquisition
- **AND** the replacement credential set's cleanup callback runs exactly once after the final attempt
