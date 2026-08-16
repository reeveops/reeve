# Drift delta

## ADDED Requirements

### Requirement: Drift owns acquired credential cleanup

Each drift stack check MUST clean its acquired credentials when the check finishes.
An auth-expiry rebind MUST clean the expired credential set before acquiring its replacement.

#### Scenario: A drift check completes

- **WHEN** a drift check acquires credentials and reaches any final outcome
- **THEN** its cleanup callback runs exactly once

#### Scenario: Expired credentials are rebound

- **WHEN** a drift check retries after an auth-expired result
- **THEN** the expired credential cleanup runs before replacement acquisition
- **AND** the replacement credential cleanup runs after the final attempt
