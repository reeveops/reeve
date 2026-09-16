# Pulumi passphrase bridge

## Why

The shared workflow accepts a Pulumi passphrase, but the child environment
boundary currently removes it before Pulumi reads stack state.

## What

- Pass the standard Pulumi passphrase variable only when engine config selects
  the passphrase secrets provider.
- Allow the configured passphrase field to use the designated environment
  reference syntax.
- Keep the passphrase inside existing redaction and child environment handling.

## Scope

- State secret environment resolution.
- Pulumi passphrase configuration and documentation.
