# Configuration

## Layout

```text
.reeve/
├── shared.yaml           # approvals, locking, bucket, freeze, comments
├── auth.yaml             # credential providers and bindings
├── notifications.yaml
├── observability.yaml
├── drift.yaml
├── pulumi.yaml           # engine: pulumi
└── terraform.yaml        # engine: terraform (or tofu.yaml, engine: tofu)
```

Config always lives in the `.reeve/` directory. There is no root-level
single-file `reeve.yaml` form.

## File convention

Every file starts with:

```yaml
version: 1
config_type: <type>
```

`config_type` values (v1): `shared`, `engine`, `auth`, `notifications`,
`observability`, `drift`, `user`.

Exactly one file per `config_type`. Multiple `engine` files (each with a
unique `engine.type`) parse individually, but validation rejects more
than one - reeve currently supports one engine per configured root; multi-engine
routing is future work.

## Validation

- Strict unmarshal - unknown keys are errors.
- Schema validation per `config_type` against Go structs in
  `internal/config/schemas/`.
- `version` is per-file - breaking changes to any schema bump only that
  file's version. Migration is handled by `reeve migrate-config`.

## User config

`~/.config/reeve/*.yaml` holds local-only preferences (`config_type: user`).
No overlap with repo config fields. CLI flags override user config overrides
defaults.

`user.yaml` currently reserves a header-only schema and does not implement rendering/local-auth preferences.
Use the documented command flags and auth bindings for those behaviors.

## CLI / config parity

Every runtime behavior has both a CLI flag and a config setting.
Flag-only exceptions are ephemeral behavior (`--dry-run`, `--verbose`,
`--explain`) or invocation identity supplied by CI (`--run-number`,
`--run-attempt`, `--run-url`, and `--sha`). No config-only behaviors.
