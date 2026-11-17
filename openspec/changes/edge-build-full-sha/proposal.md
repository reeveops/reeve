# edge-build-full-sha

## Why

Edge builds embedded only the short commit, and their assets used a rolling
name (`reeve_linux_<arch>.tar.gz`) that a re-run overwrote with `--clobber`.
Builds are not reproducible (the build date is embedded), so a re-run REPLACED
signed artifacts with differently-signed bytes - consumers who verified the
old checksums were silently broken, and a 7-hex commit prefix is grindable
(~16M work with attacker-settable committer timestamps).

## What

- Edge asset names carry the full 40-char commit SHA
  (`reeve_<sha>_linux_<arch>.tar.gz`); the tags do too (`<branch>-<sha>`),
  and the embedded `main.version`/`main.commit` ldflags use the full SHA.
- `--clobber` is gone. A release that already carries its artifacts is never
  re-uploaded; a rerun of the same commit stops with a visible note. A
  partial upload (release exists, assets missing) completes.
- The binary resolver resolves the new names: the commit kind names the asset
  from the caller's full SHA ref; the edge kind derives the SHA from the
  selected release's tag.

## Scope

- `.github/workflows/edge-build.yml`.
- `.github/scripts/fetch-binary.sh` (the asset-name resolution for edge and
  commit kinds - the resolver changes in the same PR, as the plan requires).

## Impact

- **Edge asset names change**: consumers who downloaded edge assets by their
  old rolling name must use the resolver (the documented path); the resolver
  changes here in the same PR.
- Version-tag releases are unchanged.
