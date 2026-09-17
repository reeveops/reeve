# Local Pulumi demo

Preview three stacks across two Node.js projects using the `random` provider.
This creates no cloud workload resources; Reeve artifacts and engine state stay in local directories.

## Prerequisites

Install Reeve, Pulumi, Node.js, npm, and Python 3 (used once to configure the local backend path).
Provider/package installation requires network access; the fixture passphrase is public synthetic data, not a credential for real infrastructure.

## Set up all stacks

From the Reeve checkout:

```bash
cd examples/toy-stack
export PULUMI_CONFIG_PASSPHRASE=reeve-demo-only
mkdir -p pulumi-state
python3 - <<'PY'
from pathlib import Path
config = Path('.reeve/pulumi.yaml')
backend = (Path.cwd() / 'pulumi-state').as_uri()
config.write_text(config.read_text().replace('file:///REPLACE_WITH_ABSOLUTE_PATH/pulumi-state', backend))
PY
pulumi login "file://$PWD/pulumi-state"
(cd projects/random-name && npm install && pulumi stack init dev --secrets-provider=passphrase --non-interactive && pulumi stack init prod --secrets-provider=passphrase --non-interactive)
(cd projects/random-secret && npm install && pulumi stack init dev --secrets-provider=passphrase --non-interactive)
reeve lint
reeve stacks
reeve plan-run --root . --sha demo --run-number 1
```

Run initialization once against a fresh local backend; an existing stack does not need to be initialized again.
Expect `random-name/dev`, `random-name/prod`, and `random-secret/dev`, followed by a rendered preview containing random resource additions.

`.reeve/pulumi.yaml` explicitly supplies the fixture backend and passphrase to Reeve's engine environment.
Setup records an absolute backend URL so all project directories use the same state; rerun setup from a fresh copy if you move the demo.
An ambient passphrase alone is not copied automatically into engine processes.

## Files and cleanup

The preview writes `.reeve-state/`, and setup creates `pulumi-state/`, project dependencies, and any local CLI metadata.
Use a disposable checkout; after this preview-only demo, remove its generated state/dependencies or discard the checkout, and restore any previous Pulumi backend selection you changed.

Do not copy the filesystem bucket or public passphrase into production.
For separate GitHub workflow runs, follow [getting started](../../docs/getting-started.md).

## Deeper scenarios

The evolving [reeve-test harness](https://github.com/reeveops/reeve-test) includes additional Pulumi lifecycle scenarios and diagnostic reports.
Read its current setup and coverage before choosing an E2E lane.
