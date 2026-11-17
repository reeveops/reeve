# PR flow - deltas

## ADDED Requirements

### Requirement: Cached release archives are verified before use

The action MUST cache downloaded release archives rather than extracted
binaries or locally generated hash sidecars. Every run MUST fetch the resolved
release's `checksums.txt` and cosign bundle and verify the checksum signature
before consuming a cached or freshly downloaded archive. The archive MUST
match the signed checksum before extraction, and the extracted binary MUST
pass `reeve --version` before use.

Only an archive digest mismatch against verified signed checksums is a tamper
signal that MUST fail the run. The failure MUST name the expected digest,
actual digest, and cache key, and MUST NOT extract the archive.

Checksum or bundle fetch failure, missing cosign, signature verification
failure, wrong action source, missing or unfetchable assets, extraction
failure, and a non-executable binary MUST instead fall back to a source build
with the specific reason. These paths MUST NOT consume the cached archive.
Source builds and binaries that fail the sanity check MUST NOT be cached.

Archives MUST be stored per resolved release tag under immutable GitHub
Actions cache keys. When a restore supplies tag A but resolution selects tag
B, a verified fresh B archive MUST be saved under B's key without attempting
to overwrite A's key. A later run resolving B MUST be able to restore and
reuse it.

#### Scenario: A tampered cached archive

- **GIVEN** a cached archive whose digest differs from the live verified
  checksum for the same resolved tag
- **WHEN** verification runs
- **THEN** the run fails and names the expected digest, actual digest, and
  cache key
- **AND** the archive is not extracted or executed

#### Scenario: Live checksums cannot be fetched or verified

- **GIVEN** a cached archive and a checksum fetch failure, missing bundle,
  missing cosign, or invalid signature
- **WHEN** verification runs
- **THEN** the cached archive is ignored and nothing from it is consumed
- **AND** the action falls back to a source build with the specific reason
- **AND** the source-built binary is not saved to the archive cache

#### Scenario: A verified binary does not execute

- **GIVEN** an archive that matches the verified checksum but whose extracted
  binary fails `reeve --version`
- **WHEN** the sanity check runs
- **THEN** the action falls back to a source build with that reason
- **AND** neither the binary nor its archive is newly cached

#### Scenario: The resolved tag changes after a cache restore

- **GIVEN** a restore supplied tag A's archive
- **WHEN** the resolver selects tag B and B is absent from the restored files
- **THEN** B is downloaded and verified against B's live signed checksums
- **AND** B is saved under B's own immutable cache key even though the restore
  supplied A
- **AND** A's existing cache key is not overwritten

#### Scenario: A freshly resolved tag is reused

- **GIVEN** tag B was saved after the tag change above
- **WHEN** a later run resolves B again
- **THEN** B's archive can be restored and is re-verified against live signed
  checksums
- **AND** the release asset is not downloaded again after successful
  verification
