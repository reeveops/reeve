# Blob store - deltas

## ADDED Requirements

### Requirement: Key slugging is injective, with no empty-component alias

Object keys are built from slugged project and stack components. The slug
MUST be injective: a component MUST NOT alias another distinct input. The
empty component MUST land in the transformed namespace (digest-identified)
instead of returning a fallback name verbatim, which would collide with a
component literally named like the fallback.

#### Scenario: An empty stack and a stack named like the fallback

- **GIVEN** a stack configuration with an empty stack name
- **WHEN** its lock key is derived
- **THEN** the key differs from the key of a stack literally named like the
  fallback
- **AND** every ordinary (non-empty, pass-through-safe or
  sanitize-surviving) name keeps its pre-existing key
