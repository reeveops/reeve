# Tasks

## Initial implementation

- [x] Reuse decoded lock state and version in all lock walkers.
- [x] Avoid writes for unchanged reaping and PR cleanup.
- [x] Recompute transitions after CAS conflicts and retain identity validation.
- [x] Add operation-count and concurrent-update regression tests.
- [x] Run focused tests, the local E2E suite, full checks, and strict spec validation.

## Follow-up implementations

- [ ] Add metadata listing and metadata-only retention with concurrent replacement protection.
- [ ] Reduce GCS body-read metadata requests against the pinned SDK contract.
- [ ] Add explicit maintenance commands, configuration, and migration.
- [ ] Add stage timing and request-count baselines for large run histories.
- [ ] Continue preview reuse, early action routing, binary identity, reusable workflows, and backend lanes from the delivery plan.
