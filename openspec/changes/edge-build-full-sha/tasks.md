# edge-build-full-sha - tasks

- [x] 1. Edge assets carry the full 40-char SHA; tags and embedded ldflags use the full SHA
- [x] 2. `--clobber` removed; reruns of a published commit stop with a visible note
- [x] 3. Resolver resolves the new names for edge and commit kinds
- [x] 4. Resolver test covers the new asset name (edge + commit kinds); fails against the old resolver
