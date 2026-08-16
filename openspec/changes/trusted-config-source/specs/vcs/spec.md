# VCS delta

## ADDED Requirements

### Requirement: PR metadata identifies immutable head and base commits

The VCS adapter MUST return head SHA, base SHA, and fork status from one PR metadata snapshot.
Downstream trust decisions MUST NOT replace the base SHA with a branch name resolved later.

#### Scenario: Base branch advances during a run

- **WHEN** the base branch moves after PR metadata is fetched
- **THEN** every trusted config read still uses the original base SHA

### Requirement: Trusted config files are read at an exact revision

The VCS adapter MUST read repository files at a caller-provided commit SHA and enforce the `.reeve` path boundary.
Symlinks, submodules, directories, oversized responses, and revision mismatches MUST fail closed.

#### Scenario: Trusted config is a symlink

- **WHEN** a `.reeve` entry at the trusted revision is a symlink or submodule
- **THEN** the read fails without following the target
