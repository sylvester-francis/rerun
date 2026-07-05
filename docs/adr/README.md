# Architecture decision records

These record the decisions that shaped rerun, and the alternatives rejected, so a
future reader (including a future maintainer) can see not just what rerun does but
why it does it that way. Each is short. A decision that is later reversed is
superseded, not deleted. The coding rules these imply live in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md); these capture the reasoning underneath.

Format: context, the decision, its consequences, and the alternatives weighed.

| # | Decision |
|---|---|
| [0001](0001-journal-is-the-source-of-truth.md) | Persist the result each step produced, not the position reached |
| [0002](0002-recovery-is-one-code-path.md) | Recovery is running the workflow again, not a second routine |
| [0003](0003-zero-dependency-core.md) | The core depends on the standard library and its own interfaces only |
| [0004](0004-a-crash-parks-it-does-not-fail.md) | A crash parks a run for resumption; only Cancel is terminal by request |
| [0005](0005-at-least-once-not-exactly-once.md) | At-least-once side effects, stated honestly — not "exactly once" |
| [0006](0006-store-split-three-ways.md) | Store is split into Writer, Reader, and Guarder |
| [0007](0007-panic-for-programmer-errors.md) | Panic on programmer errors (contained to the run); return operational errors |
