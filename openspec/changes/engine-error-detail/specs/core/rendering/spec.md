# Rendering delta

## ADDED Requirements

### Requirement: Multi-line errors render in a fenced block

A rendered error MUST preserve every line of the reported message.

A single-line error MUST render inline. A multi-line error MUST render in a
fenced block, so neither the message nor the surrounding layout is lost.

Fenced error text MUST be escaped so a fence sequence in engine output cannot
terminate the block, because engine errors quote resource names and property
values originating in the pull request.

#### Scenario: Engine reports a multi-line failure

- **WHEN** a stack's error contains more than one line
- **THEN** the comment renders it in a fenced block with every line intact

#### Scenario: Error contains a fence sequence

- **WHEN** an error message contains a fence sequence
- **THEN** the rendered block is not terminated early by that sequence
- **AND** the message content is still shown

#### Scenario: Single-line error

- **WHEN** a stack's error is a single line
- **THEN** it renders inline
