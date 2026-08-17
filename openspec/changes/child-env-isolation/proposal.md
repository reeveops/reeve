# child-env-isolation

## Why

IaC CLIs and policy hooks inherit reeve's complete process environment.
Repository-controlled programs can therefore receive controller-only GitHub,
notification, OIDC, and cloud credentials that were never bound to a stack.

Policy hooks also run before the independent apply gates have denied the
stack. An unapproved or otherwise blocked apply request can execute a hook.

## What

1. Build every IaC and policy subprocess environment from a small operational
   allowlist plus credentials explicitly resolved for the stack.
2. Run policy hooks only after all independent apply gates pass, then evaluate
   the policy result as the final gate.
3. Add regression tests that place sentinel credentials in reeve's environment
   and verify that child processes cannot read them.
4. Acquire `engine.state.auth_provider` explicitly for backend login and engine
   commands instead of relying on ambient backend credentials.

## Scope

- In: Pulumi, Terraform, OpenTofu, drift commands, policy hooks, state backend
  auth, and apply gate ordering.
- Out: authorization for fork execution, trusted-base configuration, state
  credential collision handling, and credential cleanup aggregation.

## Security boundary

Environment filtering prevents accidental credential inheritance. It does not
isolate hostile code from the runner's process table or filesystem.

This change does not make fork execution safe. Fork denial and authorization
must land with a worker boundary before fork code is considered contained.
