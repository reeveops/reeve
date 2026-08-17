# Design

## Child environment construction

`iac.CommandEnv` constructs a deterministic child environment from two inputs:

- an allowlist of non-credential process settings needed to launch tools;
- an explicit map supplied by reeve after auth binding resolution.

Explicit values win over allowlisted values and duplicate keys are removed.
Credential-shaped ambient variables are never copied by name or prefix.

The initial allowlist is `PATH`, temporary-directory settings, locale settings,
terminal/color settings, timezone, and TLS certificate paths. Proxy variables
are excluded because proxy URLs can contain credentials.

CI engine children share a private run-scoped `HOME` and XDG directories that
are removed when the run ends. Hooks receive a separate command-scoped home,
while local runs preserve home paths for developer CLI profiles.

Pulumi's `PULUMI_EXPERIMENTAL` and HCL's `TF_IN_AUTOMATION` remain adapter-owned
defaults. They are added through the same builder instead of appending to the
parent environment.

## State backend auth

`engine.state.auth_provider` selects one declared auth provider for backend
operations. Reeve acquires it before Pulumi login and merges its environment
into every engine operation, with stack workload credentials taking precedence.

The state credential cleanup runs at the end of preview, apply, refresh, or
drift. Multi-identity collisions that use the same environment key require a
provider-specific representation and remain a separate change.

Apply defers state credential acquisition until independent gates and policy
hooks pass. A hook therefore cannot discover a state credential file created
for the later backend login.

## Policy hook ordering

Apply evaluates preconditions once without a policy result. A blocked result
releases the lock and returns without starting any policy process.

When independent gates pass, reeve runs hooks against the stored preview and
evaluates preconditions again with the policy result. The second evaluation is
the rendered and audited result.

## Defense-in-depth limit

The subprocess runs as the same operating-system user as reeve. On some hosts,
same-user code can inspect other processes or search readable credential files.

This change is required for every execution mode but is not sufficient to
enable fork code. Fork execution requires a separate container, user, VM, or
job that receives only the approved capability set.
