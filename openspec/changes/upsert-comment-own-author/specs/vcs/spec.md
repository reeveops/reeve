# VCS - deltas

## MODIFIED Requirements

### Requirement: Comment upserts edit only reeve's own comments

A marker-based comment upsert MUST edit a comment only when the comment's
author is the account whose token the adapter authenticates as. A comment by
another author that contains the marker MUST be left alone, logged with its
author, and answered by creating reeve's own comment. When the token's
identity cannot be established, the upsert MUST fail closed: no comment is
edited or created.

#### Scenario: A foreign comment quoting the marker

- **GIVEN** a PR comment authored by a maintainer whose body contains one of
  reeve's markers
- **WHEN** the adapter upserts a board under that marker
- **THEN** the foreign comment is unchanged
- **AND** reeve creates its own comment and logs that it left the foreign
  one alone with its author

#### Scenario: The token identity cannot be resolved

- **GIVEN** the identity lookup fails and the Actions fallback does not apply
- **WHEN** the adapter upserts a marker comment
- **THEN** the upsert fails
- **AND** no comment is edited or created
