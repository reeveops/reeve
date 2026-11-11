# Approvals - deltas

## MODIFIED Requirements

### Requirement: `pr_comment` authorization (fail-closed)

Comment approvals are commit-bound only when the comment names a SHA, and the
binding is accepted ONLY when the argument is the full 40-character HEAD SHA.
A shorter prefix of HEAD - any length - MUST NOT bind to HEAD: committer
timestamps are attacker-settable, so a prefix can be ground for a commit that
did not exist when the approval was written. Any other argument binds to the
literal value and is dismissed as stale. The refusal MUST name the approver,
the failing argument, the current HEAD, and the corrective step.

#### Scenario: An approval naming a prefix of HEAD

- **GIVEN** a comment approval source comment posts `/reeve approve <7-char
  prefix of HEAD>`
- **WHEN** the approval gate evaluates on a PR whose HEAD is that commit
- **THEN** the approval does not count toward the gate
- **AND** the trace states the approval was ignored, that the argument is not
  the full 40-character SHA of HEAD, and that the commenter must re-approve
  with `/reeve approve <head>`
