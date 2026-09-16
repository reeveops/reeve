# Pulumi passphrase bridge

## Why

Pulumi passphrase configuration does not reach the isolated child environment.
State auth providers also need a defined path for this value.

## What

- Pass the standard Pulumi passphrase variable only when engine config selects
  the passphrase secrets provider.
- Accept the value from a configured literal or selected state auth provider.
- Keep ambient host values behind the acknowledged auth-provider boundary.
- Keep the passphrase inside existing redaction and child environment handling.

## Scope

- State secret environment resolution.
- Pulumi passphrase configuration and documentation.
