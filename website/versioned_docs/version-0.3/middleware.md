---
id: middleware
title: Runtime Middleware
description: Wrap a database/sql driver so every query is analyzed at runtime — entry points, every option, what is intercepted, and the Guard core integrations build on.
---

# Runtime Middleware

The runtime middleware intercepts at the `database/sql` **driver** layer, not
with a wrapper type around `*sql.DB`. That is the difference between "analyze
the calls you remembered to route through a helper" and "analyze every
statement that reaches the database" — including the ones an ORM, sqlc or a
query builder generates for you. You get a real `*sql.DB` back, and there is
no method list to keep in sync.

## Entry points

```go
import (
    "github.com/KARTIKrocks/sqlguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
)
```

| Function | Use when |
| --- | --- |
| `sqlguard.Register(name, baseDriver string, opts ...middleware.Option) error` | You open by driver name. Wraps the driver registered as `baseDriver` and registers the result as `name`; then `sql.Open(name, dsn)`. Errors if `name` is taken or `baseDriver` is unknown. |
| `sqlguard.OpenDB(c driver.Connector, opts ...middleware.Option) *sql.DB` | You already hold a `driver.Connector` (e.g. pgx's `stdlib.GetConnector`). |
| `middleware.WrapDriver(base driver.Driver, opts ...Option) driver.Driver` | You want the wrapped `driver.Driver` itself, to register under your own scheme. |
| `middleware.WrapConnector(base driver.Connector, opts ...Option) driver.Connector` | You want the wrapped connector, to pass to `sql.OpenDB` yourself. |

`sqlguard.Register` and `sqlguard.OpenDB` are thin aliases for the
`middleware` functions of the same name; import whichever reads better.

```go
_ "github.com/jackc/pgx/v5/stdlib"

sqlguard.Register("sqlguard-pg", "pgx",
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(5, 2*time.Second),
)
db, err := sql.Open("sqlguard-pg", dsn) // a plain *sql.DB
```

## Options

Every option is a `middleware.Option`. The same set is accepted by every
[integration](integrations), so there is one surface to learn.

| Option | Default | Effect |
| --- | --- | --- |
| `WithSlowQueryThreshold(d time.Duration)` | `200ms` | Report `slow-query` when a successful query's driver-measured latency reaches `d`. |
| `WithReporter(r reporter.Reporter)` | `reporter.NewConsoleReporter()` (stderr) | Where findings go. `reporter.NewJSONReporter()` is built in; implement `Report([]analyzer.Result)` for anything else. |
| `WithAnalyzer(a *analyzer.Analyzer)` | `analyzer.Default()` | Replace the rule set — typically `analyzer.DefaultWithProfile(...)` from config, or an analyzer built `WithRawQuery()`. |
| `WithParser(p analyzer.Parser)` | `analyzer.FallbackParser` | Swap in a real grammar from [`parsers/`](parsers). Applied to whichever analyzer is in use. |
| `WithN1Detection(threshold int, window time.Duration)` | off | Report `n-plus-one` when the same query fingerprint runs `threshold` times within `window`. See [N+1 detection](n-plus-one). |
| `WithFindingDedup(window time.Duration)` | `1m` | Report each (rule, fingerprint) pair at most once per window. `0` reports every occurrence. See [Noise control](noise-control). |
| `WithAnalysisCacheSize(n int)` | `1024` | Memoize static analysis per exact query string in an LRU of `n` entries. `0` disables the cache. |

Load them from a [`.sqlguard.yml`](configuration) instead of hard-coding:

```go
opts, err := config.Middleware("", ".") // "" = discover from startDir
opts = append(opts, middleware.WithParser(pgparser.New()))
sqlguard.Register("sqlguard-pg", "pgx", opts...)
```

## What is intercepted

The wrapper hand-implements the standard driver chain — `Driver` /
`DriverContext` → `Connector` → `Conn` → `Stmt` / `Tx` — so every path
`database/sql` can take is covered:

- `Query`, `QueryRow`, `Exec` and their `Context` variants, on the `*sql.DB`,
  on a `*sql.Conn`, and inside a `*sql.Tx`.
- Prepared statements: `Prepare` / `PrepareContext`, then `Stmt.Query` /
  `Stmt.Exec` and their `Context` variants. The statement text is analyzed
  on each execution (so a prepared statement in a loop still counts toward
  N+1), and the analysis cache makes the repeat essentially free.
- Anything built on top of those — sqlc, ent, sqlx, GORM, pgx-stdlib. If it
  talks to `database/sql`, it goes through the wrapper.

For each executed statement the middleware runs the static rules (through
the cache and de-duplicator), feeds the N+1 tracker, and — when the
statement succeeds — measures latency for `slow-query`. A failed query's
latency is not reported; it is not a meaningful number.

### Base-driver behaviour is preserved

Optional driver interfaces (`QueryerContext`, `ExecerContext`,
`ConnBeginTx`, `Pinger`, `SessionResetter`, `Validator`,
`NamedValueChecker`, `ConnPrepareContext`, `StmtExecContext`,
`StmtQueryContext`) are implemented on every wrapper type, but each method
forwards to the base only if the base implements that interface — otherwise
it returns `driver.ErrSkip` or the documented no-op, so `database/sql` falls
back exactly as it would for the bare driver. A driver that lacks
`QueryerContext`, for example, still gets analyzed exactly once, on the
prepare-then-execute path `database/sql` takes instead.

The wrapper never modifies the SQL text or the arguments. It observes.

## Reporters

A `reporter.Reporter` is one method:

```go
type Reporter interface {
    Report(results []analyzer.Result)
}
```

`Report` may be called concurrently from many goroutines; both built-in
reporters take a mutex around their writer. The console reporter prints:

```text
[SQLGUARD WARNING] select-star
  Query: SELECT * FROM users WHERE email = ?
  Issue: SELECT * detected. Selecting all columns can hurt performance.
  Fix:   Select only the columns you need.
```

The JSON reporter emits an array of objects with `rule`, `severity`,
`query`, `fingerprint`, `message`, `suggestion` and — in static mode —
`file` and `line`. Both accept an `io.Writer` via `NewConsoleReporterTo` /
`NewJSONReporterTo`.

Custom reporters typically forward to a structured logger or a metrics
client. `Result.Fingerprint` is designed to be the label: it is stable,
low-cardinality and never carries a literal value. See
[Redaction & fingerprints](redaction).

## `Guard`: for integration authors

`middleware.Guard` is the single analysis core. Every interception point in
the driver chain calls into one `Guard`, and every out-of-tree integration
must too — that is what makes redaction, fingerprints, the parser seam,
config, N+1, de-duplication and the cache behave identically everywhere.

```go
g := middleware.NewGuard(opts...)
```

| Method | Use |
| --- | --- |
| `Check(query string)` | Run the static rules and feed the N+1 tracker. Use when you have a single before-execution hook and no latency. |
| `CheckLatency(query string, elapsed time.Duration)` | Report `slow-query` if `elapsed` reaches the threshold. Use from an after-execution hook. |
| `Observe(query string) func(err error)` | `Check`, then start a timer. Call the returned closure when the operation completes; it reports latency only when `err == nil`. Designed for split start/end hooks — stash the closure in the `context.Context`. |
| `ResetN1()` | Clear N+1 state. Call at a request boundary. No-op when N+1 detection is off. |
| `Analyzer() *analyzer.Analyzer` | The configured analyzer, for `PrepareQuery` when you need redact/fingerprint without re-deriving policy. |

A `Guard` is safe for concurrent use. The [pgx integration](pgx) is the
reference implementation: `TraceQueryStart` calls `Observe`, stores the
closure on the context, and `TraceQueryEnd` invokes it with
`data.Err`. Hand-rolling a check/latency pair instead of using `Guard`
silently loses everything above; do not.
