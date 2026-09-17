# Policy Hooks

## Model

Generic command-execution hooks. reeve runs user-specified commands against
plan JSON and treats exit codes as pass/fail. The hook protocol is engine-agnostic; each tool integration must support its actual input shape, environment, and credentials.

## Config

Inside engine config:

```yaml
policy_hooks:
  - name: opa-compliance
    command: ["conftest", "test", "--policy", "policies/", "{{plan_json}}"]
    on_fail: block              # block | warn
    required: true

```

## Placeholders

- `{{plan_json}}` - path to the plan JSON reeve wrote.
- `{{stack_name}}`, `{{project}}`, `{{env}}` - current stack context.

## Exit code semantics

- `0` - pass.
- non-zero + `on_fail: block` - apply gate fails; stdout surfaces in PR
  comment.
- non-zero + `on_fail: warn` - warning in PR comment; apply proceeds.
- `required: false` + command not present - skip silently.

## Stdout safety

All stdout captured from hooks passes through `internal/core/redact` before
appearing in the PR comment, audit log, or any user-visible surface. No
redaction bypass.

## No dedicated config_type

Hooks live in engine config. Cross-engine OPA policies are templated into
multiple engine configs if needed. A dedicated `config_type: policy` is
not justified.

## Input and execution contract

The JSON wrapper contains `project`, `stack`, `env`, `counts`, `plan_summary`, and `plan`.
`plan` is parsed engine JSON when available, otherwise a string or null; it is not a universal policy-engine schema.

Hooks receive a constructed environment without controller or apply credentials and have a five-minute process timeout.
`on_fail: warn` treats a missing required command as advisory too; `required: false` skips only when `command[0]` is unavailable.

See the [policy guide](../../../../docs/policy-hooks.md) and tested [Conftest recipe](../../../../examples/policy-opa/README.md).
