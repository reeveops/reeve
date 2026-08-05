# policy-opa

Policy hooks backed by Conftest (OPA) plus a custom cost-gate script.
Two blocking hooks + one advisory warn hook.

## Layout

```text
examples/policy-opa/
├── .reeve/
│   └── pulumi.yaml            # policy_hooks block
├── policies/
│   ├── require_tags.rego      # OPA policy
│   └── restrict_regions.rego  # OPA policy
└── scripts/
    └── cost-gate.sh           # custom bash script, uses infracost
```

## Workflow prerequisites

Install Conftest + Infracost in the workflow:

```yaml
steps:
  - uses: actions/checkout@v6
  - uses: instrumenta/conftest-action@master
  - uses: infracost/actions/setup@v4
    with:
      api-key: ${{ secrets.INFRACOST_API_KEY }}
  - uses: reeveops/reeve@master
    with:
      command: apply
      pulumi-version: "3.231.0"
```

## How it fires

On every apply, per stack:

1. reeve writes the plan summary JSON to a temp file.
2. `opa-require-tags` runs Conftest against `require_tags.rego` - blocks
   on failure.
3. `opa-restrict-regions` runs Conftest against `restrict_regions.rego` -
   blocks on failure.
4. `cost-gate` runs `scripts/cost-gate.sh` - warns on failure (doesn't
   block, just surfaces the overrun in the PR comment).

If any `on_fail: block` hook fails, the `GatePolicy` precondition
fails and apply halts for that stack.
