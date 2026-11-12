# VCS - deltas

## MODIFIED Requirements

### Requirement: Comment upserts edit only reeve's own comments

A marker-based comment upsert MUST edit a comment only when the comment's
author is the account whose token the adapter authenticates as. A comment by
another author that contains the marker MUST be left alone, logged with its
author, and answered by creating reeve's own comment.

The token identity is resolved in this precedence, highest first:

1. the operator setting (`comments.author`, `--comment-author`),
2. the action-supplied default-token author: when the action's github-token
   input IS the runner's default `github.token`, the author is
   `github-actions[bot]` - known from the token kind, never resolved through
   `/user`,
3. `/user` (user tokens return the user; App installation tokens return
   `<slug>[bot]`).

The adapter MUST NOT guess an identity. When none of these resolve, the
upsert MUST fail closed: no comment is edited or created, and the failure
MUST be typed and name the setting that fixes it.

#### Scenario: A foreign comment quoting the marker

- **GIVEN** a PR comment authored by a maintainer whose body contains one of
  reeve's markers
- **WHEN** the adapter upserts a board under that marker
- **THEN** the foreign comment is unchanged
- **AND** reeve creates its own comment and logs that it left the foreign
  one alone with its author

#### Scenario: The default Actions token with no configuration

- **GIVEN** the action's github-token input is the runner's default
  `github.token` and no operator setting exists
- **WHEN** the adapter upserts a marker comment
- **THEN** the author is `github-actions[bot]` and reeve's own comment is
  edited without consulting `/user`

#### Scenario: A custom token whose identity cannot be resolved

- **GIVEN** a custom token, no operator setting, and a failing `/user`
- **WHEN** the adapter upserts a marker comment
- **THEN** the upsert fails with the typed refusal naming
  `comments.author` / `--comment-author`
- **AND** no comment is edited or created

#### Scenario: An explicit setting wins in both cases

- **GIVEN** an operator setting exists
- **WHEN** the token is the default Actions token or a custom token with a
  failing `/user`
- **THEN** upserts match against the declared author in both cases and
  `/user` is not consulted
