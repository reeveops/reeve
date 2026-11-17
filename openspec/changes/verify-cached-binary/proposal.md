# verify-cached-binary

## Why

A restored executable cache entry went straight to use. Binding the cache key
to the action source did not bind the restored bytes to a signed release, so a
corrupted or poisoned entry could execute. Recording a hash sidecar beside the
binary would not fix that threat: an attacker changing the cached binary could
change its sidecar in the same entry.

## What

Cache the downloaded release archive, not an extracted binary or a local hash
sidecar. On every run, fetch `checksums.txt` and its cosign bundle from the
resolved release and verify the signature before consuming an archive. The
cached or freshly downloaded archive must match the digest in those live,
verified checksums before extraction, and the extracted binary must pass
`reeve --version`.

A digest mismatch against verified checksums is a tamper signal and fails the
run, naming the expected and actual digests and cache key. Other verification
problems fall back to a source build with a stated reason: checksum or bundle
fetch failures, missing cosign, signature verification failure, a release for
the wrong action source, a missing asset, extraction failure, or a
non-executable binary. A cached archive is ignored on those soft-fallback
paths. Source builds and binaries that fail the sanity check are never cached.

Archives are stored per resolved release tag. The action restores the latest
entry under the action-source/platform/ref prefix and saves a freshly verified
new tag under its own immutable key. Thus a restore containing tag A followed
by resolution to tag B still saves B; it never attempts to overwrite A's key.
An existing B key is left unchanged, as GitHub Actions cache keys are
immutable.

## Scope

- `.github/scripts/fetch-binary.sh`: live signature/checksum verification,
  digest policy, sanity check, per-tag archive storage, and cache-save outputs.
- `.github/actions/reeve/action.yml`: archive restore/save keys and fallback
  step conditions.
- Script tests and the OpenSpec delta cover every hard-fail and soft-fallback
  row and the tag-A-to-tag-B save path.

## Impact

No config change. The cache key format changes and repopulates once. Soft
verification fallbacks pay the source-build cost on each run because source
builds are deliberately never cached.
