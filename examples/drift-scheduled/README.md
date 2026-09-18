# Scheduled drift recipe

Check critical production stacks every two hours, other production stacks every six hours, and development/experimental stacks nightly.
This recipe adds drift configuration and a workflow to an existing authenticated Pulumi consumer root; it contains no workload projects.

## Prerequisites

Use persistent Reeve storage and the engine/backend credentials from your chosen [auth recipe](../../docs/auth.md).
The example caller uses a prepared `reeve-drift` runner with bucket credentials and `PD_CHANGE_EVENTS_KEY`; it accepts `SLACK_BOT_TOKEN` through the shared workflow's named secret.

The shared workflow has no arbitrary PagerDuty-secret input.
Use that prepared runner or a custom action job if the destination needs additional environment variables.

## Bootstrap the same storage

From the configured consumer root in an environment with the required credentials:

```bash
reeve drift bootstrap --pattern "*/prod"
reeve drift bootstrap --pattern "*/dev"
reeve drift bootstrap --pattern "experiments/*"
reeve drift status
```

Keep `state_bootstrap.mode: require_manual` set; bootstrap explicitly creates the baseline without alerts.
Do not edit an uncommitted local config and expect `gh workflow run` to use it.

Review and commit the adapted configuration/workflow, then dispatch:

```bash
gh workflow run drift.yml -f schedule=prod
```

Inspect the Actions summary and `reeve drift report` against the same bucket.
Expect changed stacks to be classified, ongoing unchanged drift to remain quiet by default, and resolutions to close subscribed incidents/issues.

## Adapt and clean up

Replace stack patterns, destinations, service keys, and issue assignee logins before enabling schedules.
Use [noise filters and suppressions](../../docs/drift.md#classification-drift-noise-filtering) for intentional differences.

To retire the integration, disable its schedules and remove only dedicated trial resources after preserving needed history.
See [operations](../../docs/operations.md) for ongoing retention and lock maintenance.
