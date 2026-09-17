# Policy hooks

Run a command against each stack's preview data before applying it.
A blocking hook must pass before Reeve acquires apply credentials and invokes the engine.

## Start with one check

The [Conftest example](../examples/policy-opa/README.md) includes policies and synthetic inputs for allowed and denied changes.
Install its pinned tool version, run its checks, then adapt the policy to your workload.

Add a hook under `engine` in your engine configuration:

```yaml
policy_hooks:
  - name: prevent-production-deletes
    command: ["conftest", "test", "--policy", "policies/prevent_deletes.rego", "{{plan_json}}"]
    on_fail: block
    required: true
```

Install the policy tool on your runner before Reeve runs.
Use a prepared runner or [custom composite-action job](github-actions.md#composite-action-for-custom-jobs); the shared workflow has no arbitrary setup-step input.

## What the hook receives

`{{plan_json}}` is an absolute path to a temporary JSON file with Reeve metadata and a `plan` field:

```json
{
  "project": "api",
  "stack": "prod",
  "env": "prod",
  "counts": {"add": 1, "change": 0, "delete": 0, "replace": 0},
  "plan_summary": "Human-readable summary",
  "plan": {"resource_changes": []}
}
```

`plan` contains structured engine output when it parses as JSON, a string for other nonempty output, or null when absent.
Its contents are engine-specific and pass through Reeve's output redaction; a Terraform policy cannot assume Pulumi exposes the same resource fields.

Use `counts` for an engine-independent change-count rule.
Use structured `plan` data for resource rules and fail explicitly if the required shape or values are absent; substring matching the human summary does not prove every resource meets a policy.

| Placeholder | Value |
| --- | --- |
| `{{plan_json}}` | Absolute path to the wrapper shown above. |
| `{{project}}` | Project name, such as `api`. |
| `{{stack_name}}` | Stack/workspace name, such as `prod`. |
| `{{env}}` | Environment derived by discovery from the stack name. |

Commands are argv lists; Reeve does not interpret shell syntax unless you explicitly invoke a shell.
Relative script/policy paths resolve from the Reeve process's working directory, so run from the intended root or use paths appropriate to the caller.

## Results and defaults

| Setting or result | Behavior |
| --- | --- |
| `on_fail` omitted | `block`. |
| `required` omitted | `true`. |
| Exit `0` | Pass. |
| Nonzero exit with `on_fail: block` | Fail the policy gate and block this stack's apply. |
| Nonzero exit with `on_fail: warn` | Report a warning and permit apply if other gates pass. |
| Missing command with `required: false` | Skip the hook. |
| Missing command with `required: true` | Error handled according to `on_fail`. |
| Hook exceeds five minutes | Cancel the process and handle the error according to `on_fail`. |

`required: false` checks `command[0]` only; it does not check every tool a script invokes.
An advisory hook's missing tool is still advisory, so use `on_fail: block` for enforcement.

The PR gate trace reports blocking failures, with redacted command diagnostics.
Keep output concise; use your CI's artifact-upload mechanism for longer reports instead of printing secrets or treating a download command as an upload.

## Apply ordering and credentials

Apply evaluates fork, draft, branch, checks, preview, approvals, locks, and freeze gates before policy execution.
A stack blocked by an independent gate never launches the repository-controlled hook.

Hooks receive a constructed environment without controller or apply credentials, and CI hooks receive temporary HOME/XDG paths.
State credentials, Pulumi login, and apply credentials are resolved only after policy passes; break-glass does not change this ordering.

Local hooks retain the operator's HOME/XDG paths and are not a filesystem sandbox.
Do not rely on redaction or environment filtering as an operating-system isolation boundary.

## Engine-native policies and cost tools

Pulumi native policy packs and external cost estimators have their own setup, input, and credential requirements.
A generic Reeve hook is not a built-in integration or an assurance that an arbitrary tool command validates the saved plan.

For a Terraform JSON consumer, the engine data is nested under `.plan`, not at the wrapper root.
Validate the tool's expected schema and authenticated execution path in a separate recipe before using it as a gate; hooks do not inherit an ambient API token.

## More examples

The [policy example](../examples/policy-opa/README.md) covers a generic production-deletion block, a Terraform resource-tag check, and an advisory change-count limit.
The [scenario catalog](../examples/README.md#test-harness-and-scenarios) links to the evolving E2E harness and names its current coverage boundaries.
