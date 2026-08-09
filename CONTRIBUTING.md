# Contributing to reeve

## License

MIT. By contributing you agree your changes ship under MIT. We will never
relicense reeve. See `LICENSE`.

## Development workflow: OpenSpec

reeve is developed using [OpenSpec](https://openspec.dev/). Per-capability
behavior lives in [`openspec/specs/`](openspec/specs/).

### Small fixes

Typos, obvious bugs, minor refactors: PR directly with tests.

### Non-trivial changes

Every feature or behavior change goes through a proposal in
`openspec/changes/<name>/`:

```
openspec/changes/<name>/
├── proposal.md       # why, what, scope
├── design.md         # technical approach
├── tasks.md          # implementation checklist
└── specs/
    └── <capability>/spec.md   # ADDED / MODIFIED / REMOVED deltas
```

On merge, the proposal archives to
`openspec/changes/archive/YYYY-MM-DD-<name>/` and its delta specs fold
into `openspec/specs/`.

## Principles

1. **No control plane.** No server, no SaaS, no telemetry. Ever.
2. **Pure core, effectful edges.** `internal/core/*` imports only stdlib
   and sibling core packages. Enforced by `depguard` in `.golangci.yml`.
3. **Small interfaces at use-sites.** No giant central interface.
4. **Engine-agnostic core.** Capability detection only; never branch on
   `Engine.Name()`.
5. **Explicit over clever.** When a rule fires, the user can ask "why?"
   and get a clear trace.
6. **CLI / config parity.** Every runtime behavior has both a flag and a
   setting.
7. **Local-first testing.** Filesystem blob + injectable clock + fake VCS
   client - every CI behavior reproducible on a laptop in seconds.

## Code

- Go toolchain matching the `go` directive in `go.mod` - `mise install` handles it.
- `mise run check` green (fmt + vet + lint + vuln + sec + test).
- Golden-file tests for anything rendered (PR comments, drift reports,
  Slack blocks).
- AI-generated code is welcome per OpenSpec convention; note the agent
  and model in the PR description.

## Docs

- Nothing longer than 2 sentences; say more with less.
- No qualifier language. State the facts.
- Bullets over prose.
- No em dashes.

## Dev setup

```bash
mise install      # installs go, golangci-lint, govulncheck, gosec, hk,
                  # pkl, goreleaser, openspec - plus wires git hooks
mise run check    # one-shot: fmt + vet + lint + vuln + sec + test
```

Git hooks installed by `mise install` via the `postinstall` hook in
`mise.toml` → `hk install --mise`. Hooks defined in `hk.pkl`:

- **pre-commit:** `go fmt`, `go vet`, `golangci-lint --fix`
- **pre-push:** `go test -race`, `govulncheck`, `gosec`

### First push on a fresh clone

hk's pre-push diffs against `refs/remotes/origin/HEAD`, which doesn't
exist until the remote has an upstream branch. On a brand-new repo:

```bash
git push --no-verify                  # bypass hk for the initial push
git remote set-head origin --auto     # seed origin/HEAD
git push                              # subsequent pushes run hooks cleanly
```
