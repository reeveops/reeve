# core Specification

## Purpose

Cross-cutting core behavior that does not belong to a single sub-capability
(approvals, discovery, locking, preconditions, pr-flow, rendering).
Currently: on-demand explainability - the "why?" trace, reachable from the
PR itself.
## Requirements
### Requirement: The gate trace is available on demand from the PR

`/reeve explain` MUST post a comment rendering, for every stack the PR
maps to: the resolved approval rules, the current lock state, and a
report-only gate evaluation. `/reeve explain <project/stack>` MUST limit
the output to that stack. An unknown stack ref MUST produce an error
comment naming the valid refs.

The command MUST be read-only: no engine invocation, no lock
acquisition, no state writes, no per-stack cloud credential exchange.
It MAY read reeve's own state bucket (preview manifests, lock records)
with the bucket credentials the runner already holds.

Comment dispatch goes through the same `allowed-associations`
authorization gate as every other `/reeve` verb: an unauthorized
commenter's `/reeve explain` is skipped, not answered.

#### Scenario: Blocked contributor asks why

- **WHEN** a PR's apply was blocked and a commenter posts `/reeve explain`
- **THEN** a comment renders the per-gate trace with each gate's result
  and reason, without running preview or apply

#### Scenario: Single stack detail

- **WHEN** a commenter posts `/reeve explain random-name/prod`
- **THEN** the comment covers only `random-name/prod`: its approval
  rules as resolved, its lock state, its gate results

#### Scenario: Fork PR

- **WHEN** an authorized commenter dispatches `/reeve explain` on a fork PR
- **THEN** the command runs identically to a trusted PR, because it
  exchanges no per-stack cloud credentials and mutates nothing

#### Scenario: Repeat invocation at the same commit

- **WHEN** `/reeve explain` is posted twice on the same commit
- **THEN** the second invocation edits the first comment in place
  instead of adding another

