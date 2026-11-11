# Auth - deltas

## MODIFIED Requirements

### Requirement: Retrieved values are mapped explicitly through `env_map` and registered for literal redaction

Credential env values are registered for literal redaction NARROWLY by
provider classification. A credential SHOULD declare `SecretEnvKeys`: the env
variables whose values are credentials. Registration then covers exactly
those values, keeping region, profile-name, and path values readable. A
credential that does not classify MUST keep the fail-closed default: every
value registered (over-redact, never under-redact).

#### Scenario: Region and path values stay readable

- **GIVEN** a provider classifying its credential keys (STS keys, access
  tokens) out of a map that also carries `AWS_REGION` or a credentials-file
  path
- **WHEN** the engine prints those region or path values
- **THEN** they are shown unmasked in run output
- **AND** every classified credential value still renders as `[redacted]`

#### Scenario: An unclassified credential defaults to all-secret

- **GIVEN** a provider that does not declare `SecretEnvKeys` (secret
  managers, env passthrough)
- **WHEN** its env values are registered for redaction
- **THEN** every value is registered
