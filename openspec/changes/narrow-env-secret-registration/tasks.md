# narrow-env-secret-registration - tasks

- [x] 1. `Credential.SecretEnvKeys` with nil = all-secret fail-closed default
- [x] 2. Providers classify: STS/token keys listed; profile/region/path values opted out; secret managers and env passthrough stay all-secret
- [x] 3. Registration in preview/apply/refresh/state-login/drift plumbing uses only the marked values
- [x] 4. Tests: region and path values not masked; tokens and secret-manager values are
