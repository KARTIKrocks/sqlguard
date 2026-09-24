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
- `Analyzer.PrepareQuery` now redacts once instead of twice (the fingerprint
  is folded from the already-redacted text), which offsets most of the cost
  of the two-reading literal scan under **Security** below. `analyzer.Redact`
  costs roughly
  700 ns for a parameterized query and 1.3 µs for one full of literals,
  against a ~70 µs `Analyze`; a query with no backslash in it skips the
  second scan entirely. Benchmarks live in `analyzer/redact_bench_test.go`,
  and `analyzer/redact_fuzz_test.go` fuzzes the lexer's index arithmetic.

### Security

- **Redaction leaked literal values on backslash-escaped quotes.**
  `analyzer.Redact` honoured only the doubled-quote (`''`) escape, so a
  literal containing `\'` — the default escape in MySQL and in PostgreSQL
  `E'…'` strings — closed at the wrong quote. The lexer then desynchronised
  and emitted the *following* literal's contents as if they were query
  structure:

  ```text
  before: SELECT * FROM u WHERE a = E'it\'s' AND token = 'sk-live-abcdef'
       →  SELECT * FROM u WHERE a = E?s?sk-live-abcdef?
  after:  SELECT * FROM u WHERE a = E?
  ```

  This broke the invariant in `SECURITY.md` that no `Result` carries a raw
  literal out of the process, and it applied to `Result.Fingerprint` too —
  so leaked values could reach a metrics label. `Redact` now scans literals
  under *both* dialect readings of a backslash escape and redacts the union
  of the two, which cannot under-redact whichever reading the server uses.
  Genuinely ambiguous SQL is over-redacted (structure is lost, values are
  not); unambiguous SQL is unchanged.

- **Redaction did not recognise dollar-quoted strings or radix literals.**
  A PostgreSQL `$$body$$` / `$tag$body$tag$` string passed through
  completely untouched, and `0x4142` redacted only its leading `0`, copying
  the hex payload out as if it were an identifier. Both are now redacted.
  `$1`/`$2` bind placeholders and an unterminated `$…$` run are still left
  alone.

  `stripComments` is dollar-quote aware for the same reason: a `--` or `/*`
  inside a dollar-quoted body is data, and treating it as a comment consumed
  the body's closing `$tag$` along with it — leaving the scanner unable to
  find the end of the literal and copying the body out as query structure.
  `blankStringLiterals`, which `IsMultiStatement` is built on, deliberately
  does **not** learn about dollar quotes. That check guards `explain` on
  every dialect, and MySQL reads `$$` as ordinary identifier bytes, so
  applying Postgres' semantics there would blank a real MySQL separator out
  of existence — `UPDATE t AS $$ SET id = 1; DROP TABLE t` would reach
  `EXPLAIN` with its `;` intact, inside the read-write transaction
  `--allow-dml` uses, where a DDL statement implicit-commits past the
  rollback. The cost is that a Postgres query with a `;` inside a
  dollar-quoted body is refused by `explain`; hiding a separator is the one
  error that check cannot afford.

  Dollar-quote delimiters follow Postgres' unquoted-identifier rules, so the
  tag scan accepts non-ASCII letters (`$é$…$é$` was previously unrecognised,
  leaving its body in the clear) and rejects a `$` that continues an
  identifier (`foo$tag$v$tag$` is one name, not a literal). An unterminated
  but well-formed delimiter now runs to the end of the input rather than
  being ignored: on a truncated query the remainder is body, and leaving it
  unredacted would leak it.

  Note that where redaction is forced to over-redact, the query's
  **fingerprint changes too**, so a query whose values only sometimes contain
  a backslash groups under two fingerprints — splitting N+1 counts and
  de-duplication across both. Bind parameters avoid it; see the docs.

  A double-quoted run remains an *identifier* and is preserved, which is
  correct for ANSI/PostgreSQL and for MySQL under `ANSI_QUOTES` but not for
  MySQL's default `sql_mode`, where `"…"` is also a string literal. This is
  now called out in the `Redact` doc comment and in the docs.

- `analyzer.IsMultiStatement`, which guards `explain` against stacked
  statements, deliberately keeps the *narrowest* reading of a literal — the
  opposite fail-safe direction from `Redact`. Treating `\'` as an escape
  there would let `'a\'; DROP TABLE t; --'` read as a single literal and hide
  the second statement. This is now pinned by tests and explained in the
  function's doc comment.

### Fixed

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
