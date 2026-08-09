# AGENTS.md

Instructions for AI coding agents and automated reviewers working in this
repository. Human contributor docs live in `CONTRIBUTING.md`.

## What reeve is

- Single Go binary that runs inside the user's CI. No server, no SaaS, no
  control plane.
- On a PR it previews IaC changes, gates `/reeve apply` behind approval
  policy, and writes locks and audit records to the user's own bucket.
- Engines: Pulumi, Terraform, OpenTofu. VCS: GitHub.
- Status: alpha. Breaking config changes are allowed until 1.0;
  `reeve migrate-config` covers renames.

## Non-negotiable invariants

Violating any of these is a blocking issue, not a nit.

1. **No control plane.** No hosted service, no phone-home, no telemetry, no
   account. The capability does not exist in the code and must not be added.
2. **Pure core.** `internal/core/**` imports stdlib and sibling
   `internal/core` packages only. Enforced by the `depguard` `core-purity`
   rule in `.golangci.yml`.
3. **Gates fail closed.** A gate that errors during evaluation denies the
   apply. Never fail open.
4. **No long-lived secrets.** Credentials are short-lived and federated
   (AWS OIDC, GCP WIF, Azure federated, GitHub App). Never write a
   credential to disk, env, logs, or blob storage.
5. **Redaction is not optional.** Nothing reaching a PR comment, log line,
   Slack block, or audit entry may bypass `internal/core/redact`.
6. **Break-glass stays loud.** Every override path remains
   justification-gated and audited. No silent bypass.
7. **MIT forever.** No relicensing, no source-available pivot.

## Architecture

Pluggable providers on six axes: IaC engine, VCS, auth, blob, notification
channel, approval source. Core consumes each through an interface and never
imports a concrete provider.

- **Consumer-defined interfaces.** A consumer needing one method declares a
  one-method interface. See `notify.IssueClient` and `notify.CommentClient`.
- **SDKs stay in their provider package.** `go-github` only under
  `internal/vcs/github`; cloud SDKs only under `internal/auth` and
  `internal/blob`.
- **Never branch on provider identity.** `Name()` is display-only. Use
  capability flags (`iac.Capabilities`) or optional interface assertions
  (`notify.Grouper`).
- **Providers self-register** from `init()` into a registry. The import
  manifest lives in a separate `all` package (`internal/iac/all`,
  `internal/notify/all`) that commands blank-import.
- **Unmet optional deps return `(nil, nil)`** and are skipped. An
  unregistered `type:` is an error naming the registered set.
- **Heavy deps are build-tag gated.** The `reeve init` wizard
  (`charmbracelet/huh`) sits behind `reeve_minimal`. Any tag-gated path is
  built and tested in CI under its tag.

Known tracked violation: `internal/auth/factory` and `internal/blob/factory`
statically import every provider. The `split-builds` change fixes this. Do
not add new static provider imports to a factory.

## Layout

```
cmd/reeve/          CLI wiring, cobra commands, dependency injection
internal/core/      PURE. discovery, approvals, preconditions, locks,
                    freeze, breakglass, redact, render, summary, envref
internal/iac/       engine adapters: pulumi, hcl (terraform, tofu)
internal/vcs/       github client, codeowners parsing
internal/auth/      federated credential providers + secret managers
internal/blob/      s3, gcs, azblob, filesystem + lock store
internal/notify/    slack, pagerduty, webhook, github_issue, otel, timeline
internal/run/       run pipeline: preview, apply, refresh, explain
internal/drift/     scheduled drift detection and classification
internal/config/    schema, loading, migration, scaffolding
openspec/           specs (source of truth) and change proposals
docs/               user-facing documentation
examples/           runnable example stacks and workflows
```

## Commands

Use `mise`. Do not invoke tool binaries directly unless debugging.

```bash
mise install         # toolchain + git hooks + openspec scaffold
mise run check       # fmt + vet + lint + vuln + sec + test. Run before pushing.
mise run test        # go test -race ./...
mise run lint        # golangci-lint (enforces core purity)
mise run build       # bin/reeve
mise run demo        # runs against examples/toy-stack
mise run test:golden # regenerate internal/core/render/testdata
mise tasks           # full task list
```

Git hooks are installed by `mise install` and defined in `hk.pkl`:

- pre-commit: `go fmt`, `go vet`, `golangci-lint --fix`
- pre-push: `go test -race`, `govulncheck`, `gosec`

## Change workflow

reeve uses [OpenSpec](https://openspec.dev/). `openspec/specs/` is the source
of truth for per-capability behavior.

**Small fixes** (typos, obvious bugs, minor refactors): PR directly with
tests.

**Non-trivial changes** need a proposal first:

```
openspec/changes/<name>/
├── proposal.md              why, what, scope
├── design.md                technical approach
├── tasks.md                 implementation checklist
└── specs/<capability>/spec.md   ADDED / MODIFIED / REMOVED deltas
```

- Read the relevant `openspec/specs/<capability>/spec.md` before changing
  behavior in that capability.
- Spec requirements use MUST/MAY and carry `#### Scenario:` blocks. Match
  that format in deltas.
- Spec commits use the `spec:` prefix.
- On merge the proposal archives to
  `openspec/changes/archive/YYYY-MM-DD-<name>/` and its deltas fold into
  `openspec/specs/`.
- Behavior in code and behavior in the spec must not diverge. Change both in
  the same PR.

## Code conventions

- Go version follows the `go` directive in `go.mod`. Do not bump it in a
  feature PR.
- `gofmt` + `goimports`. Enabled linters: depguard, errcheck, govet,
  ineffassign, staticcheck, unused.
- Every runtime behavior has both a CLI flag and a config setting. Adding one
  without the other is incomplete.
- Small interfaces at use-sites. No giant central interface.
- Explicit over clever. When a rule fires, the user runs `/reeve explain` and
  gets a clear trace. Preserve that.
- Table-driven tests. `t.Parallel()` where independent. Assert on error types,
  not error strings.
- Golden-file tests for anything rendered: PR comments, drift reports, Slack
  blocks. Regenerate with `mise run test:golden`, never hand-edit.
- Local-first testing: filesystem blob, injectable clock, fake VCS client.
  Every CI behavior must reproduce on a laptop in seconds.

### gosec suppressions

`G204` (subprocess), `G304` (variable file path), and the `G7xx` taint rules
are ON. reeve legitimately shells out to IaC CLIs and reads
operator-configured paths, so existing sites carry
`#nosec <rule> -- <reason>`.

The point is that a new exec site, config-derived path, or outbound sink
cannot be added without writing down why it is safe. Never add a bare
`#nosec` with no rule ID and no reason.

## Security-sensitive paths

Changes here need extra scrutiny. Say so explicitly in the PR description.

| Path | Why |
| --- | --- |
| `internal/core/approvals/` | approval counts, CODEOWNERS resolution |
| `internal/core/preconditions/` | required checks, up-to-date base, preview freshness |
| `internal/core/breakglass/` | emergency override |
| `internal/core/freeze/` | freeze windows |
| `internal/core/locks/`, `internal/blob/locks/` | FIFO lock FSM, CAS, heartbeat |
| `internal/core/redact/` | secret leakage prevention |
| `internal/auth/` | federated credential exchange |
| `.github/workflows/`, `action.yml`, `.github/scripts/` | supply chain for every downstream user |

For `action.yml` and workflows specifically: pin action refs, keep
`permissions:` minimal, never combine `pull_request_target` with checkout of
PR head, and never skip checksum or cosign verification on a download path.

## PR conventions

- Conventional-commit style title prefix: `feat` / `fix` / `docs` /
  `refactor` / `test` / `chore`. `spec:` for OpenSpec-only changes.
- AI-generated code is welcome. Note the agent and model in the PR
  description.
- Link the OpenSpec change folder when one exists.
- `mise run check` green before pushing.

## Writing style for docs and comments

Enforced across `docs/`, `README.md`, `openspec/`, and code comments.

- Nothing longer than two sentences.
- No qualifier language. State the facts.
- Bullets over prose.
- No em dashes.

## Do not

- Add telemetry, analytics, phone-home, or a hosted component.
- Import an adapter, SDK, or `cmd` dependency into `internal/core/**`.
- Branch on `Engine.Name()` or any other provider identity string.
- Add a static provider import to `internal/auth/factory` or
  `internal/blob/factory`.
- Hand-edit files in `internal/core/render/testdata/`.
- Edit anything under `openspec/changes/archive/`.
- Bypass `internal/core/redact` on any output path.
- Add a `#nosec` without a rule ID and a reason.
- Relicense or add a non-MIT dependency without flagging it.