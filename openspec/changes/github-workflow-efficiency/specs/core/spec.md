## ADDED Requirements

### Requirement: Event classification precedes action setup

The composite action MUST classify an event before binary, checkout, authentication, or engine setup.
Every later action step MUST depend on an accepted classification.

#### Scenario: Ordinary comment

- GIVEN an issue comment whose first line is not an accepted Reeve command
- WHEN the composite action starts
- THEN it MUST skip binary, checkout, authentication, engine, and Reeve execution steps.

#### Scenario: Rejected command

- GIVEN a bot, unauthorized author, plain issue, unknown verb, or malformed command
- WHEN the composite action classifies the event
- THEN it MUST perform no later setup or command execution.

#### Scenario: Accepted command

- GIVEN an authorized pull request comment with an accepted prefix and verb
- WHEN the composite action classifies the event
- THEN it MUST preserve the command and structured arguments for execution.

#### Scenario: Explicit trusted command

- GIVEN the action receives an explicit single-line command input
- WHEN the event would not otherwise dispatch Reeve
- THEN it MUST execute the explicit command through the same guarded setup path.
