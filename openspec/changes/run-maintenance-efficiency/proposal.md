# Run maintenance efficiency

- Global lock walks read every lock more than once and rewrite unchanged locks during reaping.
- Retention also opens every run object to inspect its age, making startup cost grow with bucket history.

## Scope

- Reuse each decoded lock and its CAS version during maintenance; retry against current state after conflicts.
- Preserve holder ownership, FIFO promotion, active-holder protection, and heartbeat writes.
- Follow with metadata-only retention and explicit scheduled maintenance after defining concurrent-writer protection and migration.
- Track the full product sequence in [the delivery plan](../../../docs/plans/run-efficiency-and-ci.md).
