# Security Policy

## Supported versions

rerun is pre-1.0 (`v0.x`) and the API may change between minor versions. Only
the latest tagged release receives fixes.

| Version | Supported |
| ------- | --------- |
| latest `v0.x` | ✅ |
| older | ❌ |

## Reporting a vulnerability

Please report suspected vulnerabilities privately rather than opening a public
issue:

- Preferred: open a [GitHub security advisory](https://github.com/sylvester-francis/rerun/security/advisories/new).
- Or email the maintainer at sylvesterranjithfrancis@gmail.com.

Include a description, affected version, and a reproduction if you have one. You
can expect an acknowledgement within a few days. Once a fix is available it will
be released and the advisory published with credit, unless you prefer otherwise.

## Scope notes

rerun executes workflow code you register and serializes step results through a
`Codec` (JSON by default). Treat journal payloads as trusted data written by
your own workflows; rerun does not sandbox workflow functions or validate that a
stored payload is safe to unmarshal beyond the type check replay performs.

Known-vulnerability scanning (`govulncheck`) runs nightly in CI against the
module and its dependencies, on a `check-latest` Go toolchain so standard-library
advisories are picked up as patched releases land.
