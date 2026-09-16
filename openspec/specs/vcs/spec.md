# VCS Abstraction

## Design target

GitHub first; moderate future-proofing for GitLab. Interfaces abstract
enough to extend without pressure-testing against GitLab until its adapter
lands.

## Key interfaces (use-site)

```go
type PRReader interface {
    GetPR(ctx context.Context, number int) (*PR, error)
    ListChangedFiles(ctx context.Context, number int) ([]string, error)
    ListOpenPRsTouchingPaths(ctx context.Context, paths []string) ([]PR, error) // drift
}

type CommentPoster interface {
    UpsertComment(ctx context.Context, number int, body string, marker string) error
    Capabilities() CommentCapabilities
}

type CommentCapabilities struct {
    SupportsEdit bool
}

type CodeownersProvider interface {
    ResolveOwners(ctx context.Context, changedFiles []string) (map[string][]string, error)
}
```

## Auth

- Phase 1: `GITHUB_TOKEN` from GitHub Actions.
- Phase 4: GitHub App (`github_app` auth provider); both remain supported.

## Transport

Every reeve GitHub call (PR reads, comment upserts, issue channels) goes
through one shared rate-limit-aware retrying transport: 429s and the 403
shapes the rate limiter uses retry with bounded backoff, honoring
`Retry-After` / `X-RateLimit-Reset` when present. The policy is small and
bounded (bounded attempts, bounded max wait); anything it cannot absorb
surfaces the original error response instead of stalling a CI run behind a
long reset window. Non-idempotent requests are never retried blindly: a
POST/PATCH retries only on 429 or a secondary rate limit
(403 + `Retry-After`), where GitHub documents the request was rejected
before processing; any other non-2xx surfaces unchanged.

## Comment lookup reuse

The GitHub adapter MUST reuse one paginated issue-comment snapshot per PR for
marker lookup and stale-part cleanup within an invocation. Successful creates,
edits, and stale-part deletes MUST update that snapshot without mutating slices
or comment values already returned to callers.

Comment approval evaluation MUST fetch a fresh paginated snapshot at the gate.

#### Scenario: Repeated marker updates

- **WHEN** one invocation upserts the same or different markers on one PR
- **THEN** the adapter lists that PR's comments once and reuses the snapshot

#### Scenario: A cached comment was deleted

- **WHEN** an edit by cached comment ID returns HTTP 404
- **THEN** the adapter discards the snapshot, rediscovers the marker, and
  performs one edit or create attempt against the current state

#### Scenario: A comment approval changes during apply

- **GIVEN** an earlier status update populated the issue-comment cache
- **WHEN** the apply gate evaluates comment approvals
- **THEN** the adapter fetches current comments from GitHub
- **AND** an edited or deleted approval does not survive through the cache

## Immutable PR workload identity

The maintained action MUST resolve and checkout a full PR head commit SHA.
The CLI MUST compare that identity with PR metadata before using it for gates or artifacts.

#### Scenario: Event ref moves before checkout

- **WHEN** an event identifies a PR whose head ref can move
- **THEN** the action checks out the immutable head SHA resolved for that invocation
- **AND** it verifies the working tree before credential or engine setup

#### Scenario: Checked-out head no longer matches

- **WHEN** the current PR head differs from the action-supplied checkout SHA
- **THEN** PR commands fail closed instead of replacing the artifact identity

## GitHub Enterprise Server

The adapter honors `GITHUB_API_URL` (set by the Actions runner on both
github.com and GHES) as the API base URL, so GHES installs work without
reeve-specific configuration. Unset, or pointing at github.com, means the
public API; an invalid URL fails loudly at client construction.

## Capabilities

VCS-specific capabilities (e.g. `stale_review_dismissal`) exposed via
capability flags, never hard-coded in core.

## Fork PRs

`PR.IsFork` must be populated by every adapter. Downstream gates (apply,
credentialed runs) consume this.
