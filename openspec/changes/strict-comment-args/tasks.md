# strict-comment-args - tasks

- [x] 1. Every verb validates its comment arguments against an exact accepted set
- [x] 2. An unknown argument refuses the dispatch (non-zero exit) with the verb, the argument, and the accepted set
- [x] 3. Table test proves refusals for `--dry-rnu`, `--dry-run=true`, unknown apply/unlock/preview/explain/ready flags, and a slash-less unlock positional
- [x] 4. Control test proves every documented accepted form still dispatches
