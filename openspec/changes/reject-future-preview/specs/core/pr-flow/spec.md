# PR flow - deltas

## ADDED Requirements

### Requirement: Preview manifests are not dated in the future

A manifest whose `created_at` lies ahead of the local clock by more than a
small skew MUST be skipped at selection: it never counts as fresh and never
wins the newest-manifest election. The skip MUST be warned with the object
key and the stored stamp. When NO valid manifest remains, the lookup MUST
fail closed with a typed error naming the newest skipped manifest, its stamp,
the clock, and the re-run fix. A fresh `/reeve preview` then clears the
refusal by becoming the selected manifest - the skip is what makes the
re-run effective.

#### Scenario: A one-hour-future manifest alone

- **GIVEN** the only manifest for the commit carries a `created_at` one hour
  ahead of the clock
- **WHEN** the preview snapshot loads
- **THEN** the load fails with the typed future-stamp refusal naming the
  object key, the stamp, the clock, and `/reeve preview`
- **AND** no stack's freshness gate passes on that manifest

#### Scenario: A fresh preview clears the refusal

- **GIVEN** a refusal caused by a future-dated manifest
- **WHEN** `/reeve preview` runs and publishes a current manifest
- **THEN** the next lookup skips the future-dated manifest and selects the
  fresh one
- **AND** apply proceeds against the fresh preview

#### Scenario: A rejected manifest warns with its identity

- **WHEN** a future-dated manifest is skipped
- **THEN** the job log carries a warning naming the object key and the
  stored timestamp

#### Scenario: A minor clock offset

- **GIVEN** a manifest stamped thirty seconds ahead of the clock
- **WHEN** the preview snapshot loads
- **THEN** the manifest is accepted and indexed normally
