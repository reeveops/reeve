# PR flow - deltas

## ADDED Requirements

### Requirement: Exact comment argument sets

Every `/reeve <verb>` comment command MUST accept an exact argument set per
verb. A token outside that set MUST refuse the whole dispatch with a non-zero
exit - the job fails and the refusal names the verb, the failing argument, and
everything the verb accepts. Unknown arguments MUST NOT be silently ignored.

`approve` and `breakglass` accept their free-form payload instead of arguments:
the approval SHA is policed by the pr_comment approval source with its own
refusal, and the break-glass justification is the comment body.

#### Scenario: A typo turns a dry run into a write

- **GIVEN** a comment posts `/reeve refresh --dry-rnu`
- **WHEN** the comment event is classified
- **THEN** the dispatch refuses with a non-zero exit and nothing downstream
  runs
- **AND** the refusal says `/reeve refresh refused: unknown argument
  \`--dry-rnu\`. Accepted: --dry-run, dry-run, --all, all.`
