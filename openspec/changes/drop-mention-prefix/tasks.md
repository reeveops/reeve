# Drop the `@reeve` default — tasks

- [x] `action.yml`: `command-prefix` default becomes `"/reeve"`, with the
      reason written into the input description rather than left to a
      changelog nobody reads.
- [x] `pr_comment` approval source: zero-config fallback drops `@reeve`, so
      dispatch and approval agree on what a command looks like.
- [x] `cmd/reeve`: `REEVE_COMMAND_PREFIXES` fallback matches the action input.
- [x] Docs (README, getting-started, configuration, self-hosting) stop
      advertising mention style and point at "a handle your org owns".
- [x] `examples/aws-oidc` workflow drops its `@reeve apply` condition.
- [x] Tests: the default is slash-only; an explicitly configured `@handle`
      still works, so this is a default change and not a removal.
