# ADR-0003: A standard-library core

Status: accepted

## Context

rerun is small and correctness-critical, and it sits in the crash-recovery path of
whatever uses it. Every third-party dependency is code you did not write running
in that path, expanding the surface a reviewer must trust and the advisories you
must track.

## Decision

The core engine depends only on the Go standard library and the interfaces it
defines — `Store`, `Codec`, `Clock`, `Observer`. Backends (`sqlite`, `postgres`)
are separate packages that import `rerun`; the engine imports no backend. A new
backend changes **zero** lines of engine code.

## Consequences

- The core stays under ~1000 lines and auditable; `govulncheck` scans a tiny
  closure.
- The default SQLite backend is pure Go (`modernc.org/sqlite`), so binaries are
  static with no CGO toolchain.
- A backend's dependencies stay out of your build unless you import that backend;
  a program on the in-memory or its own store pulls nothing extra.
- Dependencies point one way only — inward, at abstractions — so the Postgres
  backend was added as a new package satisfying `Store`, engine untouched.

## Alternatives considered

- **Bake in a storage engine, or build on a workflow framework.** Rejected: it
  couples the engine to one backend, bloats the trust and vulnerability surface,
  and dilutes the "core idea, not the platform" pitch. Adding any dependency
  warrants its own ADR, not a default.
