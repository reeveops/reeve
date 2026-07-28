# CORE — command prefix delta

## MODIFIED Requirements

### Requirement: Comment commands are matched by an exact prefix

The first whitespace-delimited token of a comment's first line MUST exactly
match a configured command prefix. The default set SHALL be `/reeve` alone.

`@`-prefixed entries are supported but never defaulted: `@` in a GitHub
comment is a mention, so any accepted `@handle` notifies whoever owns that
account. A default may only contain handles reeve controls, and reeve is an
Action with no account of its own.

#### Scenario: Zero configuration

- **WHEN** `command-prefix` is not set
- **THEN** `/reeve apply` dispatches and `@reeve apply` does not

#### Scenario: An org opts into a mention style

- **WHEN** `command-prefix: "/reeve,@my-org-bot"` is configured
- **THEN** both prefixes dispatch, and the org owns the consequences of the
  handle it named

#### Scenario: The approval source agrees with dispatch

- **WHEN** the `pr_comment` approval source runs with no configured prefixes
- **THEN** its fallback set is `/reeve` only, so a comment that could not have
  dispatched a command cannot count as an approval either
