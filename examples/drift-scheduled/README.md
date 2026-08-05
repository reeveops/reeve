# drift-scheduled

Scheduled drift detection with named schedules (critical / prod /
slow-movers) and three channels: Slack dashboard channel, PagerDuty
Events API, GitHub issue tracker.

Pairs with one of the federation examples - this directory shows only
the drift-specific config plus the workflow.

## Schedules

Three tiers, each with its own cadence:

| Schedule | Cadence | Scope |
|---|---|---|
| `critical` | every 2h | payments, auth |
| `prod` | every 6h | rest of prod, excluding critical |
| `slow-movers` | nightly | dev + experiments |

## Secrets to set in the repo

```
PD_CHANGE_EVENTS_KEY          # PagerDuty v2 integration key
SLACK_BOT_TOKEN               # xoxb-... for the drift channel
```

## Bootstrap

First-ever drift run: set `state_bootstrap.mode: baseline` in
`drift.yaml`, run once to seed state, then revert to `require_manual`.

```bash
# Edit drift.yaml: state_bootstrap.mode: baseline
gh workflow run drift.yml -f schedule=prod
# Verify it seeded state:
reeve drift status
# Edit drift.yaml: state_bootstrap.mode: require_manual
git commit -am "drift: seeded baseline; require manual from here"
```
