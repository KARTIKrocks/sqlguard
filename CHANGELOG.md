# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each Go module in this repo (root, `integrations/*`, `parsers/*`) is tagged with
the same version in lockstep.

## [Unreleased]

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

[Unreleased]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/KARTIKrocks/sqlguard/releases/tag/v0.1.0
