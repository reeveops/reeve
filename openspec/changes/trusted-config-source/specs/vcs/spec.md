# VCS delta

## ADDED Requirements

### Requirement: PR metadata identifies immutable head and base commits

The VCS adapter MUST return HeadSHA, BaseSHA, target repository identity, head repository identity, and fork status from one PR metadata snapshot.
Trusted-file reads MUST use the snapshotted target repository at BaseSHA.

Downstream trust decisions MUST NOT replace BaseSHA with a branch name resolved later.
Fork PRs MUST NOT route trusted-file reads to the fork repository.

#### Scenario: Base branch advances during a run

- **WHEN** the base branch moves after PR metadata is fetched
- **THEN** every trusted config read still uses the original base SHA

#### Scenario: A fork targets the repository

- **WHEN** a fork PR snapshots target repository `owner/repo` at BaseSHA
- **THEN** trusted config reads use `owner/repo` at that exact BaseSHA
- **AND** HEAD-owned bytes remain associated with the snapshotted head repository and HeadSHA

### Requirement: Checked-out HEAD matches the PR snapshot

The checked-out commit MUST equal the snapshotted HeadSHA before any HEAD-owned bytes are parsed.
A mismatch MUST fail before merge, credential handling, network sinks, policy hooks, or engine execution.

#### Scenario: The workflow checked out a different commit

- **WHEN** the checkout commit differs from HeadSHA
- **THEN** the PR operation fails without parsing HEAD configuration

### Requirement: Trusted config files are read at an exact revision

The VCS adapter MUST read repository files at a caller-provided commit SHA and enforce the `.reeve` path boundary.
Symlinks, submodules, directories, oversized responses, and revision mismatches MUST fail closed.

#### Scenario: Trusted config is a symlink

- **WHEN** a `.reeve` entry at the trusted revision is a symlink or submodule
- **THEN** the read fails without following the target

### Requirement: Exact-revision CODEOWNERS reads use an explicit allowlist

A separate CODEOWNERS read capability MUST accept only `CODEOWNERS`, `.github/CODEOWNERS`, and `docs/CODEOWNERS` at the caller-provided SHA in the target repository. It MUST fail closed for outside, missing, or unreadable paths and MUST NOT fall back to PR HEAD. Symlink, submodule, directory, size, and revision-mismatch protections MUST remain enforced.

#### Scenario: Recognized CODEOWNERS is read at the base SHA

- **GIVEN** break-glass CODEOWNERS base locking is enabled
- **WHEN** the adapter reads `.github/CODEOWNERS` for authorization
- **THEN** the content comes from the snapshotted target repository at the exact BaseSHA
- **AND** no general read outside `.reeve` is granted to trusted-config loading

#### Scenario: A path is outside the CODEOWNERS allowlist

- **WHEN** a caller requests any path other than `CODEOWNERS`, `.github/CODEOWNERS`, or `docs/CODEOWNERS`
- **THEN** the capability rejects the read before returning content
- **AND** no alternate or normalized path bypasses the allowlist

#### Scenario: An allowlisted CODEOWNERS path cannot be read

- **WHEN** the selected allowlisted path is missing, unreadable, a directory, a symlink, a submodule, oversized, or at an unexpected revision
- **THEN** the read fails closed
- **AND** authorization does not fall back to PR HEAD or another unallowlisted path
