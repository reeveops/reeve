# `/reeve explain` tasks

- [x] Comment parser: `explain` verb + optional `project/stack` arg;
      unknown ref posts an error comment listing valid refs.
- [x] `reeve run explain` CLI parity (`--stack`, `--pr`).
- [x] Report-only gate evaluation path: every gate checks, nothing acts
      (`preconditions.EvaluateAll` + read-only input building in
      `internal/run/explain.go`).
- [x] Renderer: rules block + lock block + shared gate trace, one
      comment keyed per commit, edited in place on re-invocation.
- [x] Redaction: output goes through `internal/core/redact`.
- [x] `/reeve help` lists the new command.
- [x] Docs: getting-started command table + README command table. Zero
      new config.
- [x] Tests: report-only comment (engine never applies), lock held by
      another PR, unknown-stack error comment, fork-PR path, golden file
      for the rendered comment, `EvaluateAll` full-trace unit tests.
