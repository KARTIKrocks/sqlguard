# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each Go module in this repo (root, `integrations/*`, `parsers/*`) is tagged with
the same version in lockstep.

## [Unreleased]

### Fixed

- **The dialect parsers no longer drop findings on statements they do not
  model** ([#81]). `pgparser` and `mysqlparser` cleared every structural
  field before looking at the AST and refilled them only for
  `SELECT`/`INSERT`/`UPDATE`/`DELETE`, so anything else the grammar accepted
  lost what the fallback had found and was still marked `Exact`:
  `CREATE VIEW v AS SELECT * FROM t`, `CREATE TABLE … AS SELECT *` and
  `EXPLAIN SELECT *` all lost `select-star`. Those statements now keep the
  fallback's `Statement` with `Exact == false`. Both parsers also read the
  row source of `INSERT … SELECT *` now, and read every operand of a
  `UNION`/`INTERSECT`/`EXCEPT` instead of none of them, so
  `SELECT * FROM t UNION SELECT * FROM u` reports `select-star` and
  `select-without-limit` as the fallback does. `mysqlparser` had treated a
  `UNION` as `StmtOther`.
- **`pgparser` no longer treats a bare `OFFSET` as a `LIMIT`** ([#82]). The
  grammar builds a limit node for `OFFSET n` alone, and `HasLimit` was set
  from the node rather than from a row count, so `select-without-limit` and
  `orderby-without-limit` never fired on `SELECT a FROM t ORDER BY a OFFSET
  5000`. `LIMIT ALL` still counts as a limit.
- The fallback parser reads the `ORDER BY` of a statement wrapped in
  parentheses, `(SELECT … ORDER BY a)`, as the statement's own, as both
  grammars do. It read it as a subquery's and missed
  `orderby-without-limit`, which made `pgparser` report a finding the
  fallback did not.

### Security

- **`parsers/pgparser` no longer pulls a six-year-old gRPC stack.**
  `github.com/auxten/postgresql-parser` drags in `google.golang.org/grpc`,
  `google.golang.org/protobuf` and `github.com/sirupsen/logrus` transitively,
  and MVS was resolving them to `v1.33.1`, `v1.25.0` and `v1.6.0` — between
  them the subject of eight advisories, including a CVSS 9.1 gRPC
  authorization bypass ([CVE-2026-33186]). None was ever reachable: every gRPC
  advisory is server-side (xDS RBAC, HTTP/2 transport) and a SQL parser starts
  no gRPC server, which is why `make vuln` stayed green — `govulncheck`
  reports on called symbols, dependency scanners report on the graph. They are
  now `v1.83.2`, `v1.36.12` and `v1.9.3`, so both views agree. No API change;
  `pgparser`'s own behaviour is unaffected.
- The docs site build chain picked up the available patches for `fast-uri`,
  `image-size`, `js-yaml`, `nanoid`, `joi`, `qs`, `svgo`, `smol-toml`,
  `colord` and `serialize-javascript`. Build-time only — nothing here ships to
  consumers of the Go modules or to readers of the published site.

[#81]: https://github.com/KARTIKrocks/sqlguard/issues/81
[#82]: https://github.com/KARTIKrocks/sqlguard/issues/82

## [0.5.0] - 2026-09-25

### Fixed

- **Every query is no longer analyzed twice when the base driver returns
  `driver.ErrSkip`** ([#67]). `wConn.QueryContext`/`ExecContext` analyzed the
  query before handing it to the base. A base with no direct `Queryer`/`Execer`
  was covered, but not one that has the entry point and declines the call with
  `driver.ErrSkip`: `database/sql` then falls back to Prepare+Query, which
  re-enters through the wrapped statement and analyzes the same execution a
  second time. `go-sql-driver/mysql` returns `ErrSkip` for every parameterized
  query unless `interpolateParams=true`, which is off by default — so on MySQL
  essentially all traffic was double-analyzed. **N+1 counts were doubled**,
  halving the effective threshold (`WithN1Detection(10, …)` fired at 5 real
  queries) and producing spurious N+1 reports; duplicate static findings were
  masked by the default one-minute dedup window and only surfaced under
  `WithFindingDedup(0)`. The base is now called first and analysis is skipped
  when it answers `ErrSkip`, so the prepare path remains the single analysis
  point. On this path findings are produced after execution rather than
  before, which nothing consumes.
- **A query retried after `driver.ErrBadConn` is no longer analyzed once per
  attempt.** `database/sql` retries a failed query on another connection —
  twice from the pool, then once on a fresh connection — and every attempt
  re-entered the wrapper, so one logical query could be analyzed three times.
  `ErrBadConn`'s contract is that a driver must not return it when the
  operation may have been performed, so a declined attempt executed nothing
  and is now skipped, at the connection and the statement level alike. This is
  the same N+1 inflation as [#67] from the other per-call "did not run"
  answer, and it surfaces whenever pooled connections go stale: MySQL's
  `wait_timeout`, a server restart, a failover.
- **A statement call rejected by argument conversion is no longer analyzed.**
  `wStmt.ExecContext`/`QueryContext` ran the rules before converting named
  parameters for a base that predates them, so a call that failed with
  `sqlguard: driver does not support named parameters` — without ever
  reaching the database — still produced findings and incremented the N+1
  counter.

- **`pgparser` no longer reports `insert-without-columns` on
  `INSERT INTO t DEFAULT VALUES`** ([#68]). The grammar encodes that form as
  an absent row source, and the parser refilled `InsertColumnsListed` from
  `len(Columns)` alone after blanking it — so the AST path dropped the
  fallback's explicit handling ("DEFAULT VALUES inserts no data") and then
  marked the result `Exact`. Opting into the exact parser made this rule
  strictly worse than the zero-dependency default, inverting the trade-off
  documented in [SQL Parsers](https://kartikrocks.github.io/sqlguard/docs/parsers).
- **`insert-without-columns` now covers every keyword that inserts rows
  positionally**, not just `INSERT INTO`: MySQL/SQLite's `REPLACE`, the
  `UPSERT` the CockroachDB-derived grammar behind `pgparser` accepts, and the
  forms that omit MySQL's optional `INTO` (`INSERT t VALUES (…)`,
  `REPLACE t VALUES (…)`), including a `LOW_PRIORITY`/`DELAYED`/`IGNORE`
  modifier run. All bind by column order and carry the same schema-change
  risk, and all are the same AST node to a real grammar — so the dialect
  parsers already reported them while the fallback read them as an
  unrecognized statement kind and said nothing. `REPLACE(str, from, to)` is
  never mistaken for a statement, and a `REPLACE()` call inside a CTE does not
  displace the real statement head. A CTE prefix puts the keyword
  mid-statement, past the leading-keyword check, so
  `WITH c AS (…) UPSERT INTO t SELECT …` is covered too.
- **A column named `into` no longer defeats `insert-without-columns`.** The
  target table was found by scanning for `INTO` anywhere in the statement, so
  ``INSERT t (`into`) VALUES (1)`` — a form that omits the optional keyword —
  had its column list read as the target table and was reported as having no
  columns. The statement head is now the anchor whenever it starts the
  statement; `INTO` remains the anchor for the CTE-prefixed forms, where the
  keyword sits mid-statement.

### Added

- **Parser parity is pinned by a test.** `pgparser` and `mysqlparser` each run
  a corpus through both the dialect grammar and the fallback and assert the
  grammar never reports a rule the fallback does not
  (`TestParser_NeverAddsFindingTheFallbackDoesNot`). Opting into a real parser
  should only remove findings, which is the direction the docs promise. It is
  an invariant over a corpus rather than a proof — all four bugs above are the
  same shape, and only one of them had been noticed.

### Changed

- **`StmtInsert` now covers `REPLACE` and `UPSERT`**, which reaches
  `sqlguard explain`: both were previously refused as unrecognized statements
  and are now admitted under `--allow-dml`, planned and rolled back like any
  other DML. On a server where the keyword is not valid, the server's syntax
  error replaces sqlguard's refusal.
- **`insert-without-columns` message reworded** from "INSERT without explicit
  column list" to "Row-inserting statement without an explicit column list",
  since it no longer fires only on `INSERT`.

[#67]: https://github.com/KARTIKrocks/sqlguard/issues/67
[#68]: https://github.com/KARTIKrocks/sqlguard/issues/68

## [0.4.0] - 2026-09-25

### Changed

- **Every documented rule is now addressable in `.sqlguard.yml`.** The rules
  reference lists 21 rules, but only the 14 statement rules were registered:
  naming `slow-query`, `n-plus-one`, `seq-scan`, `high-cost`,
  `full-table-scan`, `no-index-used` or `filesort` under `disable`, `only`,
  `severity` or `settings` warned with `unknown rule`, and failed outright
  under `strict: true`. All seven are registered now, and the profile reaches
  the runtime findings in middleware and the plan findings in `explain`
  exactly as it reaches a statement rule. They are still never evaluated
  against parsed SQL, so they do not fire during `sqlguard scan`.
- **Breaking (Go API): `middleware.NewQueryTracker` takes a severity.** The
  signature is now `NewQueryTracker(threshold, window, severity, reportFn)`.
  It is exported, so a caller constructing a tracker directly will not
  compile until the argument is added; pass `analyzer.SeverityWarning` for
  the previous behaviour. Guard resolves it from the profile, which is what
  makes a `severity:` override on `n-plus-one` reach the finding.
- **Breaking (config): the slow-query threshold moved** from the top-level
  `slow-query.threshold` key to `rules.settings.slow-query.threshold`, so
  every per-rule tunable lives in one place. `Config.SlowQueryThreshold` and
  the `SlowQueryConfig` type are gone with it. An explicit
  `WithSlowQueryThreshold` in Go still wins over the file.
- **`explain` now honors `rules:` config.** It previously ignored it by
  design, which is what its docs said. A `severity` override also beats
  `seq-scan`'s row-count-derived severity.
- **`only:` is scoped to the rules evaluated against a statement.** It
  narrows the scanner and the statement rules at runtime, and deliberately
  does not reach `slow-query`, `n-plus-one` or the five plan rules. A
  whitelist is written to focus a scan and names statement rules; if it
  reached the rest, `only: [select-star]` in a repository's config would also
  switch off latency and N+1 reporting in the running application and make
  `sqlguard explain` report nothing, without naming any of them and without
  warning. `disable:` and `severity: off` reach every surface.
- **N+1 detection can be enabled from the config file.** Setting both
  `rules.settings.n-plus-one.threshold` and `.window` turns it on; it was
  previously reachable only from Go via `WithN1Detection`, which still takes
  precedence.
- A value in `rules.settings` that will not read back as its type is now
  reported **and dropped**, so the rule falls back to its built-in default
  rather than acting on the rejected value. In lenient mode the load continues
  after a warning, so reporting alone was half an answer:
  `slow-query.threshold: 0` warned and then matched every successful query
  anyway, flooding the reporter it exists to protect. This
  covers durations and numbers, and a half-specified `n-plus-one` block —
  a quoted `threshold: "10"` is a string in YAML, read back as 0, which would
  have left N+1 detection off with no indication.
- An `only:` list that names no rule the scanner runs — `only: [slow-query]`,
  now that it is a valid name — is reported. It would otherwise leave
  `sqlguard scan` finding nothing on any codebase, which reads as a clean run.
- A non-positive `slow-query.threshold` or `n-plus-one.window` is reported. A
  threshold of `0` matches every successful query, so it would have flooded
  the reporter with `slow-query` on every statement; a zero window leaves N+1
  off. A fractional-millisecond threshold such as `0.5` also truncated to zero
  when read, which produced the same flood — it now scales before converting.
- A misspelled setting **key** is reported too, not just a bad value: the rule
  name is known and the value well-formed, so the setting is simply absent and
  the built-in default stands. A `settings` block on a rule with no tunables
  (the five plan rules) is reported the same way.
- An unknown rule name in `disable:` / `only:` / `severity:` / `settings:` is
  now warned about **and ignored**, rather than warned about and honored. One
  typo in `only:` acted as a whitelist matching nothing, which since every
  rule became addressable would have silenced the runtime and plan findings
  as well as the static scan.

## [0.3.0] - 2026-09-25

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
- **CLI `scan` said nothing when a path argument was ambiguous.** A trailing
  `...` is read as the package pattern, so a real directory of that name was
  skipped and the run could exit clean without opening the tree that was
  named. The pattern still wins — that is what the go command does, and
  deciding by what is on disk would make `./q/...` stop being recursive the
  day `q/...` appeared — but the ambiguity is now reported, and a trailing
  slash (`./q/.../`) addresses the directory.
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

[Unreleased]: https://github.com/KARTIKrocks/sqlguard/compare/v0.5.0...HEAD
[CVE-2026-33186]: https://pkg.go.dev/vuln/GO-2026-4762
[0.5.0]: https://github.com/KARTIKrocks/sqlguard/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/KARTIKrocks/sqlguard/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/KARTIKrocks/sqlguard/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/KARTIKrocks/sqlguard/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/KARTIKrocks/sqlguard/releases/tag/v0.1.0
