# Approvals - deltas

## MODIFIED Requirements

### Requirement: `pr_comment` authorization (fail-closed)

A `/reeve approve` comment MUST NOT count as an approval when the comment was
edited after posting: any difference between the comment's `updated_at` and
`created_at` refuses the approval. The refusal MUST name the approver and state
the corrective step (post a new approval comment). A refusal from the approval
source MUST NOT disappear silently: evaluation renders it on the approval trace
and the approval does not count toward the gate.

#### Scenario: An edited approval comment

- **GIVEN** an authorized non-author commenter posts `/reeve approve <sha>`
- **AND** the comment's `updated_at` is later than its `created_at`
- **WHEN** the approval gate evaluates
- **THEN** the approval does not count toward the gate
- **AND** the trace states the approval was ignored because the comment was
  edited after posting, and that a new approval comment is required

#### Scenario: A re-post replaces an edited approval

- **GIVEN** an edited approval comment by a commenter
- **AND** a later, unedited approval comment by the same commenter
- **WHEN** the approval gate evaluates
- **THEN** the later comment's approval counts, and the edited comment's
  refusal does not stand
