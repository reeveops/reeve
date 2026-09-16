# Design

## Selection

`state.secrets_provider.type: passphrase` authorizes one fixed child variable,
`PULUMI_CONFIG_PASSPHRASE`.

The configured `passphrase` value wins when present. Otherwise Reeve reads the
same fixed variable from its host environment.

## Isolation

Other secrets provider types do not copy the Pulumi passphrase. The selected
value joins state auth before backend login and every engine operation.

The existing run redactor receives all state and stack environment values. It
therefore removes the passphrase from engine diagnostics and rendered output.
