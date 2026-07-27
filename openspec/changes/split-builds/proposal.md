# Split builds

## Why

The architecture spec (`openspec/specs/architecture/spec.md`) requires that a
factory never statically import every concrete provider, so a build can
compile in a subset. Two factories still violate it:

- `internal/auth/factory` imports every auth provider.
- `internal/blob/factory` imports every blob backend.

The consequence is that the AWS, GCP, and Azure SDKs link into every binary
regardless of which one a repo uses. That is the dominant contributor to
binary size, and it is dependency surface a CI binary handling cloud
credentials carries for no reason: a repo on S3 + AWS OIDC still ships the
Azure and GCP SDKs.

The `reeve_minimal` build tag exists but currently excludes only the
interactive `reeve init` wizard (`charmbracelet/huh` and its TUI tree) — a
few MB against a ~47 MB stripped binary. Until the factories self-register,
no meaningful slim artifact is possible, so there is nothing worth shipping.

## What

1. Move `auth/factory` and `blob/factory` onto the self-registration pattern
   already used by `internal/iac` and `internal/notify`: providers call
   `Register(type, ctor)` from `init()`, the factory resolves purely by the
   config `type:` string, and an `all` package holds the blank-import
   manifest that commands import.

2. Introduce per-cloud build tags so a build can select its provider set, and
   verify in CI that a sliced build links none of the excluded SDKs.

3. Ship a slim artifact once (2) makes it meaningfully smaller. Shape:
   a second `builds:` entry in `.goreleaser.yaml` producing
   `reeve-ci_<version>_<os>_<arch>`, landing in the same release so the
   existing `checksums.txt` + cosign signing covers it unchanged; a matching
   slim container variant; and `.github/scripts/fetch-binary.sh` preferring
   the slim asset with a fallback to the full one for older releases (the
   GitHub Action never needs interactive init).

4. Settle the tag vocabulary. `reeve_minimal` (mechanism) and a `reeve-ci`
   artifact are two names for one concept; converge on one before the
   artifact ships, while the tag has no external consumers.

## Non-goals

- Dropping any provider. Every provider stays available; this is about which
  ones a given build links.
- Changing config. `bucket.type` / auth provider `type:` values are unchanged;
  a build that excludes a provider fails at resolution with the existing
  "unregistered type" error naming the registered set.
