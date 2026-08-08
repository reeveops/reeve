# CORE — `/reeve explain` delta

## ADDED Requirements

### Requirement: The gate trace is available on demand from the PR

`/reeve explain` MUST post a comment rendering, for every stack the PR
maps to: the resolved approval rules, the current lock state, and a
report-only gate evaluation. `/reeve explain <project/stack>` MUST limit
the output to that stack. An unknown stack ref MUST produce an error
comment naming the valid refs.

The command MUST be read-only: no engine invocation, no lock
acquisition, no state writes, no credential exchange.

#### Scenario: Blocked contributor asks why

- **WHEN** a PR's apply was blocked and a commenter posts `/reeve explain`
- **THEN** a comment renders the per-gate trace with each gate's result
  and reason, without running preview or apply

#### Scenario: Single stack detail

- **WHEN** a commenter posts `/reeve explain random-name/prod`
- **THEN** the comment covers only `random-name/prod`: its approval
  rules as resolved, its lock state, its gate results

#### Scenario: Fork PR

- **WHEN** `/reeve explain` is dispatched on a fork PR
- **THEN** the command runs identically to a trusted PR, because it
  requires no credentials and mutates nothing

#### Scenario: Repeat invocation at the same commit

- **WHEN** `/reeve explain` is posted twice on the same commit
- **THEN** the second invocation edits the first comment in place
  instead of adding another
