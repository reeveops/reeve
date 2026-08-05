# VCS — retroactive sync delta

## ADDED Requirements

### Requirement: GitHub calls ride a rate-limit-aware transport

Every reeve GitHub call (PR reads, comment upserts, issue channels) SHALL
go through one shared retrying transport that honors GitHub rate limiting:
429s and the 403 shapes the rate limiter uses retry with bounded backoff,
honoring `Retry-After` / `X-RateLimit-Reset` when present. The policy is
small and bounded (bounded attempts, bounded max wait); anything it cannot
absorb SHALL surface the original error response instead of stalling a CI
run behind a long reset window. Non-idempotent requests SHALL never be
retried blindly: a POST/PATCH retries only on 429 or a secondary rate
limit (403 + `Retry-After`), where GitHub documents the request was
rejected before processing; any other non-2xx surfaces unchanged.

#### Scenario: Secondary rate limit recovers

- **WHEN** a comment upsert receives 403 with `Retry-After: 2`
- **THEN** the transport waits and retries, and the upsert succeeds without
  surfacing an error

#### Scenario: Long reset windows fail fast

- **WHEN** a rate-limit reset lies beyond the transport's max wait
- **THEN** the original rate-limit error surfaces to the caller immediately

### Requirement: GitHub Enterprise Server works via GITHUB_API_URL

The GitHub adapter SHALL honor `GITHUB_API_URL` (set by the Actions runner
on both github.com and GHES) as the API base URL, so GHES installs work
without extra configuration. An unset value, or one pointing at github.com,
SHALL use the public API; an invalid URL SHALL fail loudly at client
construction.

#### Scenario: GHES runner needs no config

- **WHEN** reeve runs in Actions on a GHES instance
- **THEN** all GitHub API calls target the GHES API base from
  `GITHUB_API_URL`, with no reeve-specific configuration
