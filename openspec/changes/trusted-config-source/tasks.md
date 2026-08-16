# Tasks

## Design gate

- [x] Define immutable trusted revision resolution.
- [x] Define field ownership and deny-by-default extension.
- [x] Define merge, visibility, and preview-to-apply consistency.
- [ ] Approve the OpenSpec design and ownership table.

## Implementation

- [ ] Add base SHA and exact-revision file reads to the VCS interface.
- [ ] Add strict in-memory config loading for trusted bytes.
- [ ] Implement the immutable two-source merge.
- [ ] Route every PR command through the effective configuration.
- [ ] Record trusted SHA and config provenance in manifests and audit entries.
- [ ] Invalidate previews when the trusted policy SHA changes.
- [ ] Render ignored control-setting changes without values.
- [ ] Add API failure, symlink, missing file, race, and self-modification tests.
- [ ] Add documentation for control changes and repository bootstrap.
- [ ] Run `mise run check` and strict OpenSpec validation.
