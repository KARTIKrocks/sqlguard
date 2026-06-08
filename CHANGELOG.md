# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each Go module in this repo (root, `integrations/*`, `parsers/*`) is tagged with
the same version in lockstep.

## [Unreleased]

## [0.1.0] - 2026-06-08

Initial public release.

### Added

- **Runtime middleware** that intercepts at the `database/sql` **driver** layer
  (`Register` / `OpenDB`), so any query — including those issued by ORMs and
  query builders — is analyzed and you get back a real `*sql.DB`. Zero
  third-party dependencies in the core.
- **Analyzer with 19 detection rules** across static, runtime, and EXPLAIN
  surfaces: `select-star`, `leading-wildcard`, `non-sargable-predicate`,
  `add-not-null-without-default`, `implicit-join`, `cartesian-join`,
  `in-list-too-large`, `large-offset`, `select-distinct`, `delete-without-where`,
  `update-without-where`, `insert-without-columns`, `select-without-limit`,
  `orderby-without-limit`, `n-plus-one`, `slow-query`, `seq-scan`,
  `full-table-scan`, `high-cost`.
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

[Unreleased]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/KARTIKrocks/sqlguard/releases/tag/v0.1.0
