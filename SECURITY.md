# Security policy

## Reporting a vulnerability

**Do not open a public GitHub issue for security reports.**

Use GitHub's private vulnerability reporting:
<https://github.com/reeveops/reeve/security/advisories/new>.

Expect an acknowledgement within 72 hours. We aim to triage and respond
with a remediation plan within 7 days of acknowledgement.

## Scope

In scope:

- The `reeve` binary and every package under `internal/` and `cmd/`.
- The `action.yml` GitHub Action and maintained reusable workflows.
- Release tooling (goreleaser config, signing).
- Auth provider adapters (`internal/auth/providers/*`) - especially
  anything involving credential exchange, token handling, or privilege
  escalation.
- Redaction pipeline (`internal/core/redact`) - a bypass that leaks
  secrets through stdout, audit logs, or telemetry is in scope.
- Lock state machine (`internal/core/locks` + `internal/blob/locks`) -
  concurrent-write bugs that let two PRs hold the same lock are in scope.

Out of scope:

- Issues in third-party dependencies (report upstream; we'll track via
  Renovate / Dependabot and bump when fixes land).
- Social engineering, phishing, physical attacks.
- Denial-of-service against GitHub APIs via configured reeve behavior
  (configure rate limits appropriately).
- User-side misconfiguration that doesn't violate documented invariants
  (e.g. pointing reeve at a world-writable bucket, opting into
  `env_passthrough` with long-lived secrets).

## Our posture

- **Federation preferred.** OIDC/WIF/federated providers acquire short-lived credentials with provider-configured lifetimes.
  Secret managers and explicit environment mappings can supply longer-lived values; Reeve does not rotate those secrets.
- **No phone-home.** reeve emits OpenTelemetry traces/metrics only when
  `observability.yaml` is present and enabled, and only to endpoints
  the user configures. reeve never phones home.
- **Fork apply denied by default.** `apply.allow_fork_prs` gates apply and writing refresh; it does not reduce the permissions of preview credentials.
  Configure preview identities explicitly and isolate untrusted workload execution; see [fork policy](docs/auth.md#fork-pr-policy).
- **User-visible engine output is redacted.**
  Credential literals are registered with the redactor at acquire time
  - known literal values are scrubbed from engine stdout.
  Redaction does not guarantee masking transformed values; opaque saved plans remain sensitive and require protected storage.
- **Audit log is write-once.** Entries are created with
  `If-None-Match` preconditions. Reeve rejects overwrites, but bucket administrators can still delete objects unless storage policy prevents it.

## Supported versions

Only the latest release line receives security fixes. Fixes ship as a
new release, not as backports.

| Version | Supported |
| --- | --- |
| Latest stable release | Receives security fixes as a new release; no older-line backports. |
| Older stable releases | Upgrade to the latest stable release. |
| Exact-commit candidates and branch prereleases | Beta evaluation builds; report issues, but do not assume a stable-release support commitment. |

See [Releases](https://github.com/reeveops/reeve/releases/latest) for the current stable version.
A source pin makes a candidate reproducible; it does not turn that candidate into a supported stable release.

## Supply-chain controls

- **Release signing.** goreleaser produces per-platform tarballs plus a
  sha256 `checksums.txt`, and cosign (keyless, via GitHub OIDC in
  `.github/workflows/release.yml`) signs the checksums file, publishing
  the signature as `checksums.txt.bundle`. Binaries are not individually
  signed - verify a tarball's sha256 against the signed `checksums.txt`.
- **Edge signing.** The per-push `<branch>-<sha>` prereleases that back the
  GitHub Action fast-path are cosign keyless-signed the same way
  (`checksums.txt.bundle` alongside `checksums.txt`). The action requires cosign and a valid signature before using a downloaded binary.
  Missing or failed verification falls back to building the checked action source. Edge
  prereleases are unversioned and follow a branch - pin `@vX.Y.Z` for
  reproducible, supported distribution.
- **Vulnerability scanning on every PR:**
  - `govulncheck` - Go's reachability-aware vuln scanner against the
    official Go vulnerability database.
  - `gosec` - static Go security analyzer.
  - `actions/dependency-review-action` - blocks PRs that introduce HIGH+
    CVEs in dependencies.
- **Renovate auto-updates** - weekly PRs for module bumps + GitHub
  Action digest pinning. Vulnerability alerts get a `security` label and
  bypass the schedule.
- **No external network calls at test time.** Core tests use an
  in-memory filesystem blob adapter and stubbed VCS clients.

## Local pre-commit / pre-push

reeve uses [hk](https://hk.jdx.dev/) for git hooks:

- `pre-commit`: `go fmt`, `go vet`, `golangci-lint run --fix`
- `pre-push`: `go test -race`, `govulncheck`, `gosec`

Install by running `mise install` (the `postinstall` hook wires hk).

Run the full gate manually:

```bash
mise run check          # fmt + vet + lint + vuln + sec + test
hk run check            # same, via hk
```

## Coordinated disclosure

We prefer coordinated disclosure with a 90-day default embargo. If the
issue is being actively exploited, we will ship a fix as soon as
practical and coordinate disclosure timing with the reporter.

We do not currently offer a bug bounty. Acknowledgement in release
notes and the security advisory is offered with the reporter's consent.
