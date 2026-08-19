# Private Go modules, caches, and heavy programs

Workflow-only example. It carries no `.reeve/` config because nothing here is
config-specific: pair it with whichever engine and auth example matches your
setup ([`gcp-wif/`](../gcp-wif/), [`aws-oidc/`](../aws-oidc/),
[`multi-cloud/`](../multi-cloud/)).

## What it shows

reeve runs the IaC engine with an isolated `HOME` and a narrow env allowlist, so
the engine never inherits the controller's credentials. Two consequences:

- A `git config --global` URL rewrite for private modules is invisible to the
  engine, so private module fetches fail with
  `could not read Username for 'https://github.com'`.
- `GOMODCACHE` and `GOCACHE` default inside the temp `HOME`, so a cache the job
  warmed is invisible and every module re-downloads inside the run.

Four action inputs re-supply what you opt into:

| Input | Why |
|---|---|
| `private-modules-token` | Git URL rewrite plus `GOPRIVATE`, exported to the engine |
| `go-private` | Keeps the module proxy and checksum database out of the path |
| `go-cache` | Exports the runner's resolved cache paths so a warmed cache is seen |
| `prewarm-dir` / `prewarm-plugins` | Compiles and installs plugins outside the engine's preview timeout |

Terraform and OpenTofu repos can also pin the CLI to config with
`terraform-version-from-config: "true"`, so runtime cannot drift from
`engine.binary.version`.

## The token

`private-modules-token` reaches the engine subprocess, where an IaC program on
the PR branch can read it. That is a real widening of what the engine can reach,
it is off by default, and the action warns on every run that uses it.

Scope it to the module repositories with `contents:read` and nothing else. Do
not reuse the token reeve itself uses for PR comments, which needs write
permissions.

If you would rather reeve never touch that credential, keep the git-config step
in your own workflow and forward it with the `env_passthrough` auth provider
instead. See [auth.md](../../docs/auth.md).

## Files

- `.github/workflows/reeve.yml` - PR preview and apply
- `.github/workflows/drift.yml` - scheduled drift, same handling
