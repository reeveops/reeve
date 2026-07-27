# Split builds — tasks

## S1 — self-registering factories (refactor, zero behavior change)

- [ ] `internal/blob`: add `Register(type, ctor)` + registry-backed `New`;
      move each backend's constructor into its own package `init()`.
- [ ] `internal/blob/all`: blank-import manifest; commands import it instead
      of `internal/blob/factory`.
- [ ] `internal/auth`: same treatment for providers and bindings.
- [ ] `internal/auth/all`: blank-import manifest.
- [ ] Unregistered `type:` errors naming the registered set (match
      `iac.New` / `notify.Build` wording).
- [ ] Unmet optional dependencies skip via `(nil, nil)`, per the architecture
      spec.

## S2 — build slicing

- [ ] Per-cloud build tags; document the supported combinations.
- [ ] CI: build at least one sliced configuration and assert the excluded
      SDKs are absent from the binary (e.g. `go tool nm` / `go version -m`).
- [ ] Record the size delta in the job log so regressions are visible.

## S3 — slim artifact

- [ ] Converge `reeve_minimal` / `reeve-ci` on one name.
- [ ] `.goreleaser.yaml`: second `builds:` entry + archive, same release so
      `checksums.txt` and the cosign signature cover it.
- [ ] Slim container variant in `dockers_v2`.
- [ ] `.github/scripts/fetch-binary.sh`: prefer the slim asset, fall back to
      the full one when absent (older releases, edge builds).
- [ ] `edge-build.yml`: build both flavors.
- [ ] Document the artifact matrix in `docs/self-hosting.md`.

## S4 — spec

- [ ] Update `openspec/specs/architecture/spec.md`: drop the "Known violation"
      section once S1 lands.
