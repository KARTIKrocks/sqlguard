# sqlguard

[![Go Reference](https://pkg.go.dev/badge/github.com/KARTIKrocks/sqlguard.svg)](https://pkg.go.dev/github.com/KARTIKrocks/sqlguard)
[![Go Report Card](https://goreportcard.com/badge/github.com/KARTIKrocks/sqlguard)](https://goreportcard.com/report/github.com/KARTIKrocks/sqlguard)
[![Go Version](https://img.shields.io/github/go-mod/go-version/KARTIKrocks/sqlguard)](go.mod)
[![CI](https://github.com/KARTIKrocks/sqlguard/actions/workflows/ci.yml/badge.svg)](https://github.com/KARTIKrocks/sqlguard/actions/workflows/ci.yml)
[![CodeQL](https://github.com/KARTIKrocks/sqlguard/actions/workflows/codeql.yml/badge.svg)](https://github.com/KARTIKrocks/sqlguard/actions/workflows/codeql.yml)
[![GitHub tag](https://img.shields.io/github/v/tag/KARTIKrocks/sqlguard)](https://github.com/KARTIKrocks/sqlguard/releases)
[![License](https://img.shields.io/github/license/KARTIKrocks/sqlguard)](LICENSE)
[![codecov](https://codecov.io/gh/KARTIKrocks/sqlguard/branch/main/graph/badge.svg)](https://codecov.io/gh/KARTIKrocks/sqlguard)

Production-safe SQL query analyzer for Go applications.

Detects slow queries, dangerous SQL patterns, and performance issues — both at runtime and statically. Think of it as `golangci-lint` for SQL queries.

## Install

```bash
go get github.com/KARTIKrocks/sqlguard
```

CLI tool:

```bash
go install github.com/KARTIKrocks/sqlguard/cmd/sqlguard@latest
```

## Detection Rules

| Rule                           | Severity | Description                                                                                      |
| ------------------------------ | -------- | ------------------------------------------------------------------------------------------------ |
| `select-star`                  | WARNING  | `SELECT *` — selects all columns unnecessarily                                                   |
| `leading-wildcard`             | WARNING  | `LIKE '%...'` (and `ILIKE`) — index cannot be used                                               |
| `non-sargable-predicate`       | WARNING  | `WHERE LOWER(col) = ...` — function on column defeats its index                                  |
| `add-not-null-without-default` | WARNING  | `ALTER TABLE ... ADD COLUMN ... NOT NULL` without `DEFAULT` — fails / rewrites a populated table |
| `implicit-join`                | WARNING  | `FROM a, b` — comma join; a forgotten condition becomes a cartesian product                      |
| `cartesian-join`               | WARNING  | Multiple tables with no join condition or `WHERE` — a cartesian product (incl. `CROSS JOIN`)     |
| `in-list-too-large`            | WARNING  | `IN (...)` value list with more than `max-length` (default 100) elements                         |
| `large-offset`                 | WARNING  | `OFFSET` above `threshold` (default 1000) — deep pagination scans/discards skipped rows          |
| `select-distinct`              | INFO     | `SELECT DISTINCT` — often masks duplicate rows from an unintended join                           |
| `delete-without-where`         | CRITICAL | `DELETE` without `WHERE` — deletes all rows                                                      |
| `update-without-where`         | CRITICAL | `UPDATE` without `WHERE` — updates all rows                                                      |
| `insert-without-columns`       | WARNING  | `INSERT` without an explicit column list (`VALUES` or `... SELECT`) — breaks on schema change    |
| `select-without-limit`         | WARNING  | `SELECT` without `LIMIT` or `WHERE` — may return excessive rows                                  |
| `orderby-without-limit`        | INFO     | `ORDER BY` without `LIMIT` — sorts entire result set                                             |
| `n-plus-one`                   | WARNING  | Same query pattern repeated N times (runtime only)                                               |
| `slow-query`                   | WARNING  | Query exceeds latency threshold (runtime only)                                                   |
| `seq-scan`                     | WARNING  | Sequential scan detected via EXPLAIN (postgres)                                                  |
| `full-table-scan`              | WARNING  | Full table scan detected via EXPLAIN (mysql)                                                     |
| `high-cost`                    | WARNING  | High cost operation in query plan                                                                |
| `no-index-used`                | WARNING  | No index used for a table access detected via EXPLAIN (mysql)                                    |
| `filesort`                     | INFO     | `Using filesort` in the query plan — `ORDER BY` not covered by an index (mysql)                  |

## Configuration

Drop a `.sqlguard.yml` at your project root. sqlguard discovers it by walking
up from the scanned (or working) directory until it finds the file or the git
root. The CLI takes `--config <path>` and `--no-config`; the file is optional
— without it every rule runs at its default. A fully-commented template lives
at [`.sqlguard.example.yml`](.sqlguard.example.yml).

```yaml
version: 1
rules:
  disable: [orderby-without-limit]
  severity:
    select-star: info # info | warning | critical | off
    select-without-limit: "off" # "off" disables the rule
  settings:
    leading-wildcard:
      min-length: 3 # ignore short LIKE '%x%' patterns
    in-list-too-large:
      max-length: 100 # flag IN (...) lists longer than this
    large-offset:
      threshold: 1000 # flag literal OFFSET above this
redact: true # redact literals out of Result.Query (default)
slow-query:
  threshold: 200ms # runtime middleware threshold
dedup:
  window: 1m # report each repeated finding at most once per window ("0" disables)
scan:
  exclude-paths: ["(^|/)legacy/"] # static scanner only, regex
```

Unknown keys and rule names are warnings, not errors, so a config written for
a newer sqlguard still loads on an older binary; set `strict: true` to make
them fatal. `only: [rule, ...]` switches to whitelist mode.

**Inline suppressions** — no config required:

```sql
SELECT * FROM users          -- sqlguard:ignore
DELETE FROM users            /* sqlguard:ignore:delete-without-where */
```

```go
// sqlguard:ignore
db.Exec("DELETE FROM users")
db.Query("SELECT * FROM users") // sqlguard:ignore:select-star
```

In-SQL directives work at runtime _and_ in the static scanner; the Go-source
form is honored by the scanner when it sits on or directly above the call.

Apply the same config to the runtime middleware:

```go
opts, _ := config.Middleware("", ".")        // discover from cwd
sqlguard.Register("sqlguard-pg", "pgx", opts...)
```

## Security & redaction

sqlguard's findings flow into logs, so by **default it never emits raw
literal values**. Before any `Result` leaves the process its `Query` is
redacted — single-quoted strings and numeric literals become `?`, while
keywords, identifiers (including `"quoted"` / `` `backtick` `` names) and
structure are preserved:

```
[SQLGUARD WARNING] select-star
  Query: SELECT * FROM users WHERE email = ?
```

Every `Result` also carries a `Fingerprint`: the redacted query with
whitespace collapsed and `IN (?, ?, ?)` folded to `(?)`. It is a stable,
PII-free, low-cardinality identity — safe as a metrics label or log key, and
the same value the N+1 detector groups on. The JSON reporter emits it as
`fingerprint`.

Opt out only where the query text is trusted (local debugging):

```go
a := analyzer.Default().WithRawQuery()        // standalone analyzer
sqlguard.Register("pg", "pgx", middleware.WithAnalyzer(a))
```

or `redact: false` in `.sqlguard.yml`. `Fingerprint` is populated either way.

## Usage

### Runtime Middleware

sqlguard wraps at the `database/sql` **driver** layer, so you get back a real
`*sql.DB` and every query is analyzed automatically — including queries issued
by ORMs and query builders (sqlc, ent, sqlx, gorm, pgx-stdlib). There is no
wrapper type to thread through your code and no method list to keep in sync.

```go
import (
    "database/sql"
    "github.com/KARTIKrocks/sqlguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "time"
)

func main() {
    // Register an analyzed driver by wrapping an existing one...
    sqlguard.Register("sqlguard-pg", "postgres",
        middleware.WithSlowQueryThreshold(500*time.Millisecond),
        middleware.WithN1Detection(5, 2*time.Second),
    )
    db, _ := sql.Open("sqlguard-pg", "...") // db is a plain *sql.DB

    // ...or wrap a driver.Connector directly (e.g. pgx stdlib):
    //   db := sqlguard.OpenDB(connector, middleware.WithN1Detection(5, time.Second))

    // Use as normal — warnings are logged automatically
    db.Query("SELECT * FROM users")
    // Output:
    // [SQLGUARD WARNING] select-star
    //   Query: SELECT * FROM users
    //   Issue: SELECT * detected. Selecting all columns can hurt performance.
    //   Fix:   Select only the columns you need.
}
```

### N+1 Query Detection

The middleware detects when the same query pattern executes repeatedly — a classic N+1 problem:

```go
sqlguard.Register("sqlguard-pg", "postgres",
    middleware.WithN1Detection(5, 2*time.Second), // flag after 5 similar queries in 2s
)
db, _ := sql.Open("sqlguard-pg", "...")
```

N+1 patterns are detected within the configured time window. On the raw
`database/sql` driver path you get back a plain `*sql.DB`, so detection is
process-wide (windowed) — there is no handle to scope it per request. The
integration adapters (`gormguard`, `pgxguard`, `sqlxguard`, `bunguard`,
`xormguard`, `entguard`) hold the guard and expose `ResetN1()` to scope
detection to a single unit of work; call it at a request boundary.

### Noise control (finding de-duplication)

A recurring query would otherwise re-emit the same static warning on every
execution. By default the runtime middleware reports each finding (rule + query
fingerprint) **at most once per minute**, so a hot query doesn't flood your
logs. Tune or disable it:

```go
sqlguard.Register("sqlguard-pg", "postgres",
    middleware.WithFindingDedup(5*time.Minute), // quieter
)
sqlguard.Register("sqlguard-pg", "postgres",
    middleware.WithFindingDedup(0), // disable: report every occurrence
)
```

Or set `dedup.window` in `.sqlguard.yml`. Slow-query and N+1 findings have
their own emission policy and are unaffected.

The middleware also memoizes analysis per distinct query string (an LRU keyed on
the exact query — correct even for the literal-sensitive rules), so a recurring
query is parsed and rule-checked once rather than on every execution. A repeated
query then costs a cache lookup instead of a full parse (≈1000× cheaper, zero
allocations in the repeat case). Default 1024 entries; tune with
`middleware.WithAnalysisCacheSize(n)` or disable with `n == 0`.

### CLI Static Scanner

Scan your Go source code for SQL issues without running the application:

```bash
# Scan current directory
sqlguard scan .

# Scan specific package
sqlguard scan ./internal/repository

# JSON output (for CI pipelines)
sqlguard scan --format json ./...
```

Exit code is **1** when issues are found, **0** when clean — works with CI/CD pipelines.

### EXPLAIN Plan Analyzer

Connect to a live database and analyze query plans:

```bash
# PostgreSQL
sqlguard explain --db "postgres://user:pass@localhost/mydb?sslmode=disable" \
    "SELECT * FROM orders WHERE user_id = 42"

# MySQL
sqlguard explain --dialect mysql --db "user:pass@tcp(localhost:3306)/mydb" \
    "SELECT * FROM orders WHERE user_id = 42"

# JSON output
sqlguard explain --db "..." --format json "SELECT * FROM orders"
```

Detects sequential scans, missing indexes, filesort, and high-cost operations.

For safety the EXPLAIN runs inside a **transaction that is always rolled back**,
and `ANALYZE` is never used — the statement is planned, never executed. Input
is validated with a comment- and string-literal-aware multi-statement check (a
`;` hidden in a comment or string can't smuggle a second statement). Only
`SELECT`/`WITH` is allowed by default; pass `--allow-dml` to EXPLAIN an
`INSERT/UPDATE/DELETE` (still rolled back). DDL/`SET`/transaction-control is
always refused.

The transaction is additionally **read-only** everywhere except MySQL/MariaDB
under `--allow-dml`: those servers reject *every* statement in a read-only
transaction (error 1792), including an EXPLAIN that only plans it. There the
guarantee rests on the validation, on plain `EXPLAIN` never executing the
statement, and on the unconditional rollback.

MariaDB works through `--dialect mysql`. The MySQL plan is requested as
`EXPLAIN FORMAT=TRADITIONAL`, since MySQL 9 defaults `@@explain_format` to
`TREE`.

### GORM Integration

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/gormguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/gormguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
)

gormDB, _ := gorm.Open(postgres.Open(dsn), &gorm.Config{})

// Register as GORM plugin — hooks into all queries automatically
gormguard.Register(gormDB)

// Or customize via the standard middleware options
gormguard.Register(gormDB,
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)
```

### sqlx Integration

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/sqlxguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/sqlxguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
)

sqlxDB := sqlx.MustConnect("postgres", dsn)

db := sqlxguard.WrapSqlx(sqlxDB,
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
)

var users []User
db.Select(&users, "SELECT * FROM users") // warns about SELECT *
```

### pgx Integration (native pgx / pgxpool)

The `database/sql` driver wrapper covers pgx-stdlib (`pgx/v5/stdlib`). For the
**native pgx APIs** (`pgxpool.Pool`, `pgx.Conn` — which bypass `database/sql`
entirely) use `pgxguard`. It hooks pgx's own tracer seam, so every
`Query`/`QueryRow`/`Exec` and every `SendBatch` is analyzed without a wrapper
type or a method list.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/pgxguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/pgxguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "github.com/jackc/pgx/v5/pgxpool"
)

cfg, _ := pgxpool.ParseConfig(dsn)
pgxguard.ApplyPool(cfg,
    middleware.WithSlowQueryThreshold(50*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)
pool, _ := pgxpool.NewWithConfig(ctx, cfg)
```

`Apply` (for `*pgx.ConnConfig`) and `ApplyPool` (for `*pgxpool.Config`)
**compose** with any tracer already installed via pgx's own `multitracer`,
so sqlguard coexists with `otelpgx`, `ddtrace` and friends rather than
silently overwriting them. Configuration is the standard `middleware.Option`
set — same as the driver wrapper, no parallel surface to learn.

Coverage: `Query` / `QueryRow` / `Exec` (via `pgx.QueryTracer`) and
`SendBatch` (via `pgx.BatchTracer`). Prepared-statement execution is already
covered by `QueryTracer`, so `PrepareTracer` is deliberately omitted to avoid
double-reporting. `CopyFrom` carries no SQL and is out of scope.

### bun / xorm Integrations

bun and xorm build SQL through their own query layers and expose native
before/after hook seams. `bunguard` and `xormguard` plug into those seams and
run every statement through the same shared core — same `middleware.Option`
set, no parallel surface.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/bunguard
go get github.com/KARTIKrocks/sqlguard/integrations/xormguard
```

```go
// bun — register a QueryHook
db.AddQueryHook(bunguard.New(
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
))

// xorm — register a Hook
engine.AddHook(xormguard.New(
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
))
```

### ent Integration

ent runs on `database/sql`, so the simplest coverage is to point `entsql` at a
`*sql.DB` from `sqlguard.Register`/`OpenDB`. `entguard` is the dedicated
alternative: it decorates ent's own `dialect.Driver`, so it covers every
`Exec`/`Query` (and transactions it opens) regardless of how the `*sql.DB` was
created.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/entguard
```

```go
drv, _ := entsql.Open(dialect.Postgres, dsn)
guarded := entguard.Wrap(drv,
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)
client := ent.NewClient(ent.Driver(guarded))
```

Every adapter (`gormguard`, `bunguard`, `xormguard`, `entguard`, `pgxguard`,
`sqlxguard`) exposes a `ResetN1()` you can call at a per-request boundary to
scope N+1 detection to one unit of work.

### SQL Parsers (accuracy vs. zero dependencies)

By default the analyzer uses a **zero-dependency fallback parser**: it strips
SQL comments and string-literal contents before pattern matching, so keywords
inside comments/strings and identifiers like `update_at` no longer cause false
positives. It never errors — SQL it can't fully understand still yields a
best-effort result, so analysis never breaks your query path.

For **exact, structural analysis**, opt into a real grammar. These live in
separate modules so the core stays dependency-free:

```bash
go get github.com/KARTIKrocks/sqlguard/parsers/pgparser     # PostgreSQL (pure Go, no cgo)
go get github.com/KARTIKrocks/sqlguard/parsers/mysqlparser  # MySQL (pure Go, no cgo)
```

```go
import (
    "github.com/KARTIKrocks/sqlguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "github.com/KARTIKrocks/sqlguard/parsers/pgparser"
)

sqlguard.Register("sqlguard-pg", "pgx", middleware.WithParser(pgparser.New()))
db, _ := sql.Open("sqlguard-pg", dsn)

// Or with the standalone analyzer:
a := analyzer.Default().WithParser(pgparser.New())
```

A real parser drives the false-positive-prone facts (statement kind,
WHERE/LIMIT/ORDER BY/FROM presence, `SELECT *`, `SELECT DISTINCT`, `OFFSET`,
explicit INSERT columns) from the AST instead of regex. CTEs, subqueries, and
dialect syntax are handled correctly; anything the grammar rejects (dynamic SQL,
driver placeholders) transparently degrades to the fallback parser.

A few facts stay lexical heuristics even with a real parser, because they read
literal values the AST discards or are intentionally text-level: IN-list size
(`in-list-too-large`), comma/cartesian joins (`implicit-join` /
`cartesian-join`), and the literal/text checks (`leading-wildcard`,
`non-sargable-predicate`, `add-not-null-without-default`). These keep their
zero-dependency, best-effort behavior regardless of the parser.

### Custom Rules

```go
import "github.com/KARTIKrocks/sqlguard/analyzer"

// Create analyzer with only the rules you want
a := analyzer.New(
    analyzer.CheckDeleteWithoutWhere,
    analyzer.CheckUpdateWithoutWhere,
)

// Or use all defaults
a := analyzer.Default()

// Analyze a query
results := a.Analyze("DELETE FROM users")
for _, r := range results {
    fmt.Printf("[%s] %s: %s\n", r.Severity, r.RuleName, r.Message)
}
```

## Development

```bash
make help       # List all targets
make all        # tidy, fmt, vet, lint, build, test (all modules)
make build      # Compile all modules; `make cli` builds bin/sqlguard
make test       # Run tests across all modules (test-race adds -race)
make lint       # Run golangci-lint across all modules
make fmt        # gofmt -s + goimports
make tidy       # go mod tidy across all modules
make install    # Install the CLI to $GOPATH/bin
```

## Coverage

The middleware wraps the `database/sql` **driver** chain, so _every_ query
is analyzed regardless of how it's issued (`Query`/`Exec`/`Prepare`/`Tx`,
context variants, and any ORM/query builder on top — sqlc, ent, sqlx, gorm,
pgx-stdlib). There is no method allowlist to keep in sync; you get back a
real `*sql.DB`.

Opt-in adapter modules, each built on the same `middleware.Guard` core,
extend coverage to APIs that bypass or sit above the `database/sql` driver
path:

- **`pgxguard`** — native pgx / pgxpool (which never goes through
  `database/sql`), via pgx's own tracer seam. Composes with existing tracers
  (otelpgx, ddtrace) via `multitracer`. Covers `Query`/`QueryRow`/`Exec` and
  `SendBatch`.
- **`gormguard`** / **`bunguard`** / **`xormguard`** — hook each ORM's native
  before/after callback seam (`gorm.Plugin`, `bun.QueryHook`, xorm
  `contexts.Hook`).
- **`entguard`** — decorates ent's `dialect.Driver` (Exec/Query + the
  transactions it opens).
- **`sqlxguard`** — sqlx-only helpers that build SQL outside the driver path:
  `Select` / `SelectContext`, `Get` / `GetContext`, `Queryx`, `NamedExec` /
  `NamedExecContext`.

All six inherit redaction-by-default, stable fingerprints, the parser seam,
and slow-query/N+1 detection from the shared core, and expose `ResetN1()` for
per-request scoping.

## Limitations

- The static scanner resolves inline literals, same/cross-package constants,
  constant concatenation, and `fmt.Sprintf` with a constant format string
  (via `go/types`); it cannot resolve values only known at runtime.
- The default fallback parser is best-effort; for exact structural analysis use a real parser module (see _SQL Parsers_ above)
- EXPLAIN analyzer requires a live database connection; only the `postgres` and `mysql` dialects are supported (`mysql` also covers MariaDB)

## License

[MIT](LICENSE)
