# verify-cached-binary - tasks

- [x] 1. Save records the binary's sha256 sidecar and caches the directory
- [x] 2. Restore verifies the restored binary against the sidecar; mismatch or incompleteness deletes the entry and reports verified=false
- [x] 3. Rebuild-path steps run when verification failed; a rejected entry never executes
- [x] 4. Action security test asserts the mismatch path (verify conditions, sidecar recording, directory cache, rebuild conditions)
