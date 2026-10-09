#ADR-002: Stack and compilation restrictions

- Date: 2026-10-02
- Status: Accepted
- Review: reconsider if a requirement arises that only one CGO driver
SQLite can comply (see "Reopening condition")

## Context

The `ai-usage-exporter` deliverable is a single binary that an operator
download and run directly — no runtime, no mandatory container,
no additional installation steps — on darwin, linux and windows, each
in amd64 and arm64 (6 combinations). One of the planned providers (OpenCode)
reads an SQLite database (`~/.local/share/opencode/opencode.db`).

The most popular SQLite driver in Go, `mattn/go-sqlite3`, uses CGO: link
the SQLite C library. That requires an available C toolchain (and
configured for the target architecture) in each environment where it is compiled,
which breaks the cross-compilation of a single `go build` per platform and
complicates any CI pipeline that does not have that toolchain pre-installed
for the 6 target combinations.

## Decision

- **Go 1.25 or higher** as minimum toolchain version (`go.mod` fixed
`go 1.25`).
- **`CGO_ENABLED=0` is required**, not a style preference. Applies
explicitly in `make build` and `make cross`, and is checked in CI.
- As a direct consequence, the SQLite driver for the provider
OpenCode must be **`modernc.org/sqlite`** (pure implementation in Go,
transpiled from SQLite in C). **Never** `mattn/go-sqlite3` or any other
driver that requires CGO.
- Runtime dependencies allowed today, without extending this ADR:
- `github.com/prometheus/client_golang` — official metrics client
Prometheus is the de facto standard and avoids reimplementing the format of
exposure and scraping server.
- `modernc.org/sqlite` — only pure SQLite driver in Go with support
mature read-only mode and WAL, required for the provider
OpenCode without breaking the CGO restriction.
- `github.com/spf13/cobra` — CLI subcommand structure
(`serve`, `version`, etc.) when the binary needs them; avoid
reimplement flag parsing and subcommand help by hand.

Any runtime dependencies outside of this list are justified
expanding this ADR in the corresponding issue before adding it to the
`go.mod`.

## Discarded alternatives

**`mattn/go-sqlite3` (or any CGO-based driver).** It is more mature and
predictably faster than a pure driver in Go, but requires
`CGO_ENABLED=1` and a C toolchain per target platform. That contradicts
directly the requirement for a single cross-compiled binary without
external dependencies at build time, and complicates CI (images with
C toolchains for 6 OS/arch combinations instead of just the C toolchain
Go). Discarded as long as the single binary without CGO is a requirement of the
product.

## Consequences

- `make cross` should fail the build if any packages in the path
compilation requires CGO; In practice this is verified by compiling with
Explicit `CGO_ENABLED=0` for all 6 `GOOS`/`GOARCH` combinations.
- The OpenCode provider pays the cost of performance and relative maturity of
`modernc.org/sqlite` vs. a CGO driver; is accepted because the volume of
expected data (local logs from a single user) does not make it a neck
bottle.
- Any new runtime dependency (not just test) requires
justification written in the issue that introduces it before doing merge.

## Reopening condition

Reopen this decision if a requirement appears that only one CGO driver
SQLite may satisfy (for example, a performance limitation or
compatibility of `modernc.org/sqlite` that blocks the OpenCode provider) and
the team decides to accept the cost of requiring a C toolchain in the pipeline
of build for that case.
