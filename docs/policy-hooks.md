# Policy hooks

reeve does not integrate any specific policy system natively. Instead, it
runs user-specified commands against a plan JSON file and treats exit
codes as pass/fail. This keeps reeve engine-agnostic while supporting
**any** policy system: OPA, Conftest, CrossGuard, Sentinel, custom
scripts.

## Mental model

Before apply runs for a stack, reeve:

1. Writes the stack's plan to a temp JSON file.
2. Executes each configured `policy_hooks` entry with template
   substitution for `{{plan_json}}`, `{{stack_name}}`, `{{project}}`,
   `{{env}}`.
3. Treats exit code `0` as pass; non-zero behavior depends on `on_fail`.
4. Aggregates results into the `GatePolicy` precondition - any
   `on_fail: block` hook failing blocks apply.
5. Surfaces each hook's redacted stdout in the PR comment's "Policy"
   section.

The same hooks fire for every stack independently. Hook output passes
through the central redaction pipeline before landing anywhere
user-visible.

## Config

Hooks live in the engine config (e.g. `.reeve/pulumi.yaml`), not a
separate file:

```yaml
version: 1
config_type: engine

engine:
  type: pulumi
  # ... stacks, filters, etc.

  policy_hooks:
    - name: opa-compliance
      command: ["conftest", "test", "--policy", "policies/", "{{plan_json}}"]
      on_fail: block
      required: true

    - name: crossguard
      command: ["pulumi", "policy", "validate", "policies/aws-compliance"]
      on_fail: block
      required: false       # skip silently if pulumi binary is not present

    - name: cost-check
      command: ["./scripts/cost-gate.sh", "{{plan_json}}"]
      on_fail: warn         # shows in comment but does NOT block
```

### Field semantics

| Field | Required | Default | Notes |
|---|---|---|---|
| `name` | yes | - | Identifies the hook in the PR comment and OTEL metrics |
| `command` | yes | - | `argv`-style list, first element is the binary |
| `on_fail` | no | `block` | `block` blocks apply; `warn` shows in comment only |
| `required` | no | `true` | `false` silently skips if `command[0]` isn't on PATH |

### Template placeholders

- `{{plan_json}}` - absolute path to a JSON file reeve wrote for this
  stack
- `{{stack_name}}` - e.g. `prod`
- `{{project}}` - e.g. `api`
- `{{env}}` - derived from the stack name (e.g. `prod` from `prod-us-east`)

### Exit-code semantics

| Exit code | `on_fail: block` | `on_fail: warn` |
|---|---|---|
| `0` | pass (silent) | pass (silent) |
| non-zero | **fail** - apply blocked, stdout in PR comment | **warn** - stdout in PR comment, apply proceeds |
| command missing + `required: false` | skipped silently | skipped silently |
| command missing + `required: true` | **fail** with clear error | **fail** with clear error |

## Recipes

### OPA + Conftest

Put Rego policies under `policies/` in the repo:

```rego
# policies/prod_requires_tags.rego
package main

deny[msg] {
  input.project == "api"
  input.env == "prod"
  count(input.counts) > 0
  not input.plan_summary_contains_required_tags
  msg := "prod api resources must carry cost-center tag"
}
```

Hook:

```yaml
policy_hooks:
  - name: opa-conftest
    command: ["conftest", "test", "--policy", "policies/", "{{plan_json}}"]
    on_fail: block
    required: true
```

Install conftest in the workflow:

```yaml
- uses: instrumenta/conftest-action@master
- uses: FynxLabs/reeve@master
  with:
    command: apply
```

### Pulumi CrossGuard

CrossGuard policies live in a sibling `pulumi-policy-<name>/` project:

```yaml
policy_hooks:
  - name: crossguard-aws
    command:
      - pulumi
      - policy
      - enable
      - aws-compliance
      - "--stack={{stack_name}}"
    on_fail: block
    required: true
```

For CrossGuard to run inline during `preview`/`apply`, enable the
policy pack on the stack instead (reeve's shell-out to Pulumi will
honor enabled packs). This hook form is for ad-hoc validation runs
outside the normal preview flow.

### Cost gate (custom script)

```bash
#!/usr/bin/env bash
# scripts/cost-gate.sh
set -euo pipefail

PLAN="$1"
MAX_MONTHLY_DELTA_USD="${MAX_MONTHLY_DELTA_USD:-500}"

# Pipe the plan through Infracost (or your own estimator).
DELTA=$(infracost breakdown --path "$PLAN" --format json \
  | jq '.projects[].diff.totalMonthlyCost | tonumber')

if (( $(echo "$DELTA > $MAX_MONTHLY_DELTA_USD" | bc -l) )); then
  echo "monthly cost delta \$$DELTA exceeds cap \$$MAX_MONTHLY_DELTA_USD"
  exit 2
fi
```

```yaml
policy_hooks:
  - name: cost-gate
    command: ["./scripts/cost-gate.sh", "{{plan_json}}"]
    on_fail: warn     # surface in comment but don't block small overruns
```

### Per-environment policies

Use the `{{env}}` template:

```yaml
policy_hooks:
  - name: env-policies
    command: ["conftest", "test", "--policy", "policies/{{env}}/", "{{plan_json}}"]
    on_fail: block
    required: true
```

With `policies/prod/`, `policies/staging/`, etc.

### Cross-engine shared policies

Policy hooks live in engine config. reeve currently supports one engine
config per repo (loading more than one is a validation error), so today
this only matters across *repos* - but the same pattern applies: the
same OPA policies template into each engine file. Within one file, a
YAML anchor removes duplication:

```yaml
# .reeve/pulumi.yaml
_shared: &shared_policy_hooks
  - name: opa-compliance
    command: ["conftest", "test", "--policy", "../../policies/", "{{plan_json}}"]
    on_fail: block
    required: true

engine:
  type: pulumi
  policy_hooks: *shared_policy_hooks
```

(YAML anchors work within a single file. For true cross-file sharing,
a small pre-processing step in your CI is usually cleaner.)

## Redaction

Every hook's stdout and stderr pass through `internal/core/redact`
before appearing anywhere user-visible. This scrubs:

- Pulumi's `[secret]` and `<secret>` markers
- Any credential literal that the auth provider surfaced to the engine
  (reeve registers every env-var value with the redactor at acquire
  time)
- Default credential patterns (AWS access keys, GitHub tokens, Slack
  tokens - see `internal/run/redact_helper.go`)

If your policy engine prints secrets, reeve will mask them. But **don't
rely on redaction as a security boundary** - scope policy engines so
they don't have access to secrets in the first place.

## In the PR comment

A blocked stack gets a "Policy" section:

```markdown
🔐 api/prod apply gates:
  ✅ up_to_date: branch up-to-date with base
  ✅ checks_green: required checks passing
  ✅ preview_succeeded: preview succeeded
  ✅ preview_fresh: preview age 3m within window 2h
  ❌ policy: one or more policy hooks failed
  ...

Policy:
  ❌ opa-compliance
    ```
    FAIL - prod api resources must carry cost-center tag (1 violation)
    ```
  ✅ crossguard
  ⚠️ cost-gate: monthly cost delta $612.30 exceeds cap $500
```

## No dedicated `config_type: policy`

The hook model is simple enough that it doesn't justify its own config
file. Per-engine policy tooling is already engine-specific (Pulumi
policy packs, Terraform Sentinel, etc.), and shared OPA policies
template into multiple engine configs via YAML anchors.

## Troubleshooting

### Hook fires but `{{plan_json}}` is empty / missing

The plan JSON is populated with a summary object containing project,
stack, env, counts, and a short resource summary - not the raw engine
plan. Policies that need the full resource body should currently read
the engine's own artifacts (Pulumi checkpoint, Terraform plan file)
rather than the reeve-written plan.

### `required: false` hook runs anyway

`required: false` only affects the "binary not on PATH" check. If
`command[0]` exists, the hook runs. To conditionally disable a hook,
gate it at CI-config level instead (comment it out, or use a
make/script wrapper that decides).

### Policy stdout truncated at 2000 chars

Intentional - prevents a noisy hook from blowing up the PR comment.
Write to a file and link to it if you need more:

```yaml
- name: detailed-opa
  command:
    - bash
    - -c
    - |
      conftest test --policy policies/ "{{plan_json}}" \
        --output json > /tmp/policy.json
      gh run download --name policy-report /tmp/policy.json || true
      exit $(jq '[.[] | select(.failures)] | length' /tmp/policy.json)
```
