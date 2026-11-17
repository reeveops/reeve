# narrow-env-secret-registration

## Why

Redaction registration masked EVERY auth env value. That included region
values (`AWS_REGION`), profile names, and credential file paths, which are
operator-meaningful: engine output showed `[redacted]` where useful values
should be readable, hurting `reeve explain` and debugging more than it
protected anything.

## What

`Credential` gains `SecretEnvKeys`: the env variables whose values ARE
credentials. Providers classify explicitly:

- classified subsets: awsoidc (the three STS values), gcpwif (the two access
  tokens), azurefed (the two access tokens), githubapp (the token), and the
  empty set for profile/ADC providers (profile names, region values, and
  credential file PATHS are not secret values).
- unclassified (fail closed, every value registered): the secret-manager
  providers and `env_passthrough` - their values are credentials by
  construction.

The redaction registration in preview, apply, refresh, state login, and drift
mask only provider-marked values. Stop-and-report condition checked: every
provider can classify.

## Scope

- `internal/auth` (Credential field + provider classification).
- The run plumbing that registers env values into the redactor
  (`resolveAuthEnv`/`resolveStateAuthEnv` and their callers).

## Impact

- No config change.
- Engine output shows more non-secret text (region and path values); every
  credential value still masks as `[redacted]`.
