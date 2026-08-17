# action-input-hardening

## Why

The composite action embeds caller-controlled inputs directly in a Bash script.
Shell metacharacters in an input can become script syntax before Bash starts.

Workflow dependencies also use mutable major-version tags, and the binary
fast-path accepts checksum-only downloads when cosign is unavailable.

## What

1. Pass every action input through the step environment.
2. Convert commands and extra arguments to quoted Bash arrays.
3. Pin every action and workflow dependency to a full commit SHA.
4. Require cosign verification for downloaded binaries and fall back to a
   source build when verification is unavailable.

## Scope

- In: `action.yml`, repository workflows, binary fetch verification, tests,
  and operator documentation.
- Out: fork authorization policy and runtime credential isolation.
