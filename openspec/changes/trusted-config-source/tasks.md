# Tasks

## Design gate

- [x] Define immutable trusted revision resolution.
- [x] Define field ownership and deny-by-default extension.
- [x] Define merge, visibility, and preview-to-apply consistency.
- [x] Resolve the break-glass ownership conflict: the authorization surface (`break_glass.*` + its CODEOWNERS source) is trusted-resolved; `reject_self_authorization` keeps enforceable meaning (deny in self-modifying PRs). Recorded in design.md with its delta in specs/core/approvals.
- [ ] Approve the OpenSpec design and ownership table (maintainer review - this PR).

## Implementation

- [ ] Add repository identities, head and base SHAs, and exact-revision file reads to the VCS interface.
- [ ] Add strict in-memory config loading for trusted bytes.
- [ ] Implement the immutable two-source merge.
- [ ] Route every PR command through the effective configuration.
- [ ] Record `trusted_config_revision` and ownership-group provenance in manifests and audit entries.
- [ ] Invalidate previews when BaseSHA differs from `trusted_config_revision`.
- [ ] Render ignored control-setting changes without values.
- [ ] Add target-repository routing and checkout-HeadSHA mismatch tests.
- [ ] Add API failure, missing-file, symlink, and submodule tests.
- [ ] Add directory, oversized-response, and revision-mismatch tests.
- [ ] Add matched-stack preservation and missing-template tests.
- [ ] Add duplicate identity, deletion, null, empty-value, race, and self-modification tests.
- [ ] Add documentation for control changes and repository bootstrap.
- [ ] Run `mise run check` and strict OpenSpec validation.
