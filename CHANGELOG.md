# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each Go module in this repo (root, `integrations/*`, `parsers/*`) is tagged with
the same version in lockstep.

## [Unreleased]

### Added

- **Documentation site** at <https://kartikrocks.github.io/sqlguard/>, built
  with Docusaurus from `website/` and deployed from `main` by
  `.github/workflows/docs.yml`. Docs are versioned by snapshot (`0.2` is the
  first); see `website/VERSIONING.md`. `make lint-docs` lints every Markdown
  file in the repo (`.markdownlint-cli2.jsonc`) and is part of `make ci` and
  the CI workflow.
- Project logo, favicon and social card under `website/static/img/`.

### Changed

- README restructured as a landing page: logo, "Why sqlguard?" comparison,
  quick start, and a guide index pointing at the docs site. The deep
  per-feature sections moved to the site.
- Issue templates are now GitHub issue forms (`.yml`) with structured fields
  for the entry surface, parser and versions.
- `goimports` pinned to v0.50.0 (was v0.45.0).
- Dependabot also tracks the docs site's npm dependencies, grouped so
  Docusaurus bumps do not bury the Go module PRs.
- `Analyzer.PrepareQuery` redacts once instead of twice (the fingerprint is
  folded from the already-redacted text), which offsets most of the cost of
  the wider literal scan under **Security** below.

### Security

- **Redaction leaked literal values into `Result.Query` and
  `Result.Fingerprint`**, breaking the `SECURITY.md` invariant that no
  `Result` carries a raw literal out of the process. Three cases: a literal
  containing `\'` — the default escape on MySQL and in PostgreSQL `E'…'`
  strings — closed at the wrong quote and spilled the *following* literal's
  contents out as query structure; PostgreSQL dollar-quoted strings
  (`$$…$$`, `$tag$…$tag$`, including non-ASCII tags) passed through
  untouched; and `0x4142` redacted only its leading `0`. All three are now
  redacted, as are binary (`0b…`) literals. Audit any log sink or metrics
  label that already captured findings from such queries.
- **Redaction now over-redacts where the dialect is ambiguous.** Dialects
  disagree about whether `\'` closes a literal, so `Redact` covers both
  readings and blanks the union: structure may be lost around a backslash,
  but no literal survives either reading. **This changes the fingerprint
  too** — a query whose values only sometimes contain a backslash now groups
  under two fingerprints, splitting N+1 counts and de-duplication across
  both. Bind parameters avoid it.
- **`explain` refuses more input.** A `;` now counts as a statement
  separator whenever any dialect reading leaves it outside a literal, so a
  single statement carrying a `;` inside a dollar-quoted body is rejected.
  Neither reading of `$$` was safe alone: one hid a separator behind an
  unterminated ordinary literal, the other behind an unterminated dollar
  body, and either would have reached `EXPLAIN` with the separator intact
  inside the read-write transaction `--allow-dml` uses.
- **Known gap**: a double-quoted run is still treated as an identifier and
  preserved. That is correct for ANSI/PostgreSQL and for MySQL under
  `ANSI_QUOTES`, but MySQL's default `sql_mode` reads `"…"` as a string
  literal, so those values are not redacted. Use `'…'` or bind parameters on
  MySQL. Tracked in #62.

### Fixed

- **CLI JSON output went to stderr**, so `--format json > findings.json` — the
  only reason the format exists — wrote an empty file, and piping into `jq`
  saw nothing. `scan` and `explain` now write JSON to stdout; the console
  format and the summary lines stay on stderr. A clean run also emits `[]`
  instead of nothing, so a consumer is never handed an empty file to parse.
- **CLI `scan` rejected the `./...` path every doc example uses**, failing with
  `scan failed: lstat ./...: no such file or directory`. The scan has always
  been recursive, so the pattern suffix is now trimmed and `./pkg/...` selects
  exactly what `./pkg` does.
- **CLI `explain` could not connect to any database**: the binary linked no
  SQL driver, so every invocation failed with
  `sql: unknown driver "postgres" (forgotten import?)`. `cmd/sqlguard` now
  imports `github.com/jackc/pgx/v5/stdlib` and
  `github.com/go-sql-driver/mysql`. Only the CLI package imports them, so
  library consumers of `analyzer`/`middleware` do not compile them in.

## [0.2.0] - 2026-09-14

Raises the minimum Go version. No public API changed.

### Changed

- **Minimum Go version is now 1.27** (was 1.26) across all nine modules and
  `go.work`. `golangci-lint` is repinned to v2.13.2 — v2.12.2's bundled
  `staticcheck` panics building IR for stdlib/vendor packages under a Go 1.27
  toolchain.
- CI now runs `govulncheck` per module (`make vuln`, wired into `make ci`) and
  resolves both `golangci-lint` and `govulncheck` versions from the Makefile
  instead of a second hardcoded copy in the workflow, so they can't drift
  apart. Added `make tidy-check` for CI hygiene.
- `.golangci.yml` gained `errname`, `nilnesserr`, `contextcheck`,
  `fatcontext`, `noctx`, `durationcheck`, `perfsprint`, `makezero`,
  `wastedassign`, `asasalint`, `reassign`, `copyloopvar`, `intrange`, and
  `nolintlint`, plus `errcheck.check-type-assertions`.

### Fixed

- **CLI `explain`**: the initial connectivity check (`db.Ping`) ignored the
  command's 30s timeout and could hang indefinitely against an unreachable
  database. It now shares the same context-scoped timeout as the rest of the
  command.

## [0.1.1] - 2026-07-09

Fixes `explain` against current MySQL and MariaDB servers, where it previously
failed on every query. No public API changed.

### Fixed

- **`explain` on MySQL 9**: the plan is now requested as
  `EXPLAIN FORMAT=TRADITIONAL`. MySQL 9 defaults `@@explain_format` to `TREE`,
  which returns a single free-text column instead of the tabular plan, so every
  `Analyze` call failed to scan. The clause is also accepted by MySQL 5.7/8 and
  MariaDB, so no server version detection is needed.
- **`explain` on MariaDB**: plan columns are read by name rather than by
  position. MariaDB emits ten columns where MySQL emits twelve (no
  `partitions`, no `filtered`), which made the positional scan fail.
- **`WithAllowDML` on MySQL and MariaDB**: both reject *every* statement inside
  a `READ ONLY` transaction (error 1792), including an `EXPLAIN` that only plans
  it. The MySQL path now uses a regular transaction; safety still comes from
  input validation, plain `EXPLAIN` never executing the statement, and the
  transaction always being rolled back. PostgreSQL is unaffected and keeps its
  read-only transaction.
- **False positive on `UNION`**: a `UNION RESULT` row names a temporary table
  (`<union1,2>`) with `type=ALL` and no key, and was reported as an unindexed
  full table scan. Rows naming a derived or temporary table are now skipped.
- **Fails closed on an unrecognized MySQL plan**: if the server returns a plan
  without the expected columns, `Analyze` now returns an error naming the
  missing column instead of silently reporting zero issues. A false negative in
  a query linter is worse than a hard failure.

### Added

- `test/integration`: an unpublished module that runs `explain` against live
  PostgreSQL, MySQL and MariaDB (`make db-up && make test-integration`). The
  tabular `EXPLAIN` output it parses is version-dependent, so no unit test can
  guard these regressions. Kept as its own module so the database drivers stay
  out of the core import graph, and behind an `integration` build tag so the
  default `go test ./...` needs no Docker.

### Changed

- Local development now uses a committed `go.work` instead of per-module
  `replace` directives. Without it the satellite modules compiled against the
  *published* core even in CI, so a breaking change to `analyzer/` or
  `middleware/` could pass a green build. Consumers are unaffected; use
  `GOWORK=off` to reproduce a consumer's build. `make release-prep` and its
  `replace`-restoring dance are gone — releasing is documented in
  `CONTRIBUTING.md`.

## [0.1.0] - 2026-06-08

Initial public release.

### Added

- **Runtime middleware** that intercepts at the `database/sql` **driver** layer
  (`Register` / `OpenDB`), so any query — including those issued by ORMs and
  query builders — is analyzed and you get back a real `*sql.DB`. Zero
  third-party dependencies in the core.
- **Analyzer with 21 detection rules** across static, runtime, and EXPLAIN
  surfaces: `select-star`, `leading-wildcard`, `non-sargable-predicate`,
  `add-not-null-without-default`, `implicit-join`, `cartesian-join`,
  `in-list-too-large`, `large-offset`, `select-distinct`, `delete-without-where`,
  `update-without-where`, `insert-without-columns`, `select-without-limit`,
  `orderby-without-limit`, `n-plus-one`, `slow-query`, `seq-scan`,
  `full-table-scan`, `high-cost`, `no-index-used`, `filesort`.
- **Redaction by default**: every `Result.Query` is redacted (literals → `?`)
  before it leaves the process, and every `Result.Fingerprint` is a PII-free,
  low-cardinality query identity safe as a metric label. Opt out with
  `WithRawQuery()` / `redact: false`.
- **N+1 detection** (windowed) and **slow-query detection** with configurable
  thresholds.
- **Finding de-duplication** — each finding (rule + fingerprint) is reported at
  most once per window (default 1m) to keep hot queries from flooding logs
  (`WithFindingDedup`).
- **Per-query analysis cache** — an LRU keyed on the exact query string so
  recurring queries are parsed and checked once (`WithAnalysisCacheSize`).
- **Pluggable parser**: a zero-dependency, never-erroring `FallbackParser` by
  default, with opt-in real grammars in separate modules — `parsers/pgparser`
  (PostgreSQL) and `parsers/mysqlparser` (MySQL) — via `WithParser`.
- **File configuration** (`.sqlguard.yml`, discovered up to the git root):
  enable/disable rules, `only` whitelist, per-rule severity overrides, per-rule
  settings, `redact`, `slow-query`, `dedup`, and scanner `exclude-paths`.
  Lenient by default; `strict: true` makes unknown keys/rules fatal.
- **Inline suppressions** — in-SQL `-- sqlguard:ignore[:rules]` (honored at
  runtime and statically) and Go-source `// sqlguard:ignore[:rules]` (honored by
  the scanner).
- **CLI** (`cmd/sqlguard`): `scan` for static analysis of Go source (with
  literal/constant resolution via `go/types`) and `explain` for live EXPLAIN
  plan analysis. `explain` never executes the statement — it validates input and
  runs inside an always-rolled-back read-only transaction.
- **ORM / driver integrations**, each a separate opt-in module built on the
  shared `middleware.Guard` core (redaction, fingerprints, parser seam,
  slow-query, N+1, and a `ResetN1()` per-request hook): `integrations/gormguard`,
  `integrations/sqlxguard`, `integrations/pgxguard` (native pgx / pgxpool),
  `integrations/bunguard`, `integrations/xormguard`, `integrations/entguard`.

[Unreleased]: https://github.com/KARTIKrocks/sqlguard/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/KARTIKrocks/sqlguard/releases/tag/v0.1.0
