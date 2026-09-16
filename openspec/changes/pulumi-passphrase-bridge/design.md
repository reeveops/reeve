# Design

## Selection

`state.secrets_provider.type: passphrase` authorizes one fixed child variable,
`PULUMI_CONFIG_PASSPHRASE`.

The configured `passphrase` value wins when present. Reeve does not expand an
environment reference in this PR-controlled field.

A configured state auth provider may emit the same fixed variable. Reading a
host value requires the existing acknowledged `env_passthrough` provider.

## Isolation

Other secrets provider types do not copy the Pulumi passphrase. The selected
value joins state auth before backend login and every engine operation.

The existing run redactor receives all state and stack environment values. It
therefore removes the passphrase from engine diagnostics and rendered output.
