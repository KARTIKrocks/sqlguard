---
id: sqlx
title: sqlx
description: sqlxguard — wrap a *sqlx.DB so its Select, Get, Queryx and NamedExec helpers are analyzed, or layer sqlx over the driver wrapper for full coverage.
---

# sqlx

sqlx builds on `database/sql`, so there are two ways to cover it. Pick one
per connection.

## Option A: the driver wrapper (full coverage)

Register the wrapped driver and hand sqlx the resulting `*sql.DB`. Every
sqlx method — `QueryRowx`, `NamedQuery`, `MustExec`, `Beginx`, all of it —
is covered, because interception happens beneath sqlx:

```go
sqlguard.Register("sqlguard-pg", "pgx", opts...)
sqlDB, err := sql.Open("sqlguard-pg", dsn)
db := sqlx.NewDb(sqlDB, "pgx")
```

This is the recommended path unless you need `ResetN1()`.

## Option B: `sqlxguard` (helpers + `ResetN1`)

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/sqlxguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/sqlxguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "github.com/jmoiron/sqlx"
)

sqlxDB := sqlx.MustConnect("pgx", dsn)

db := sqlxguard.WrapSqlx(sqlxDB,
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)

var users []User
err := db.Select(&users, "SELECT * FROM users") // warns: select-star
```

### API

| Symbol | Use |
| --- | --- |
| `sqlxguard.WrapSqlx(db *sqlx.DB, opts ...middleware.Option) *WrappedDB` | Wrap. Panics on a nil `*sqlx.DB`. |
| `(*WrappedDB).DB() *sqlx.DB` | The underlying connection, for methods the wrapper does not expose. |
| `(*WrappedDB).ResetN1()` | Clear N+1 state at a request boundary. |
| `Select`, `SelectContext`, `Get`, `GetContext`, `Queryx` | sqlx helpers, analyzed. |
| `NamedExec`, `NamedExecContext` | Named-parameter exec, analyzed. |
| `Query`, `QueryContext`, `Exec`, `ExecContext` | `database/sql` passthroughs, analyzed. |
| `Ping`, `Close` | Passthroughs. |

Everything you call through `db.DB()` bypasses analysis. That is the
trade-off of a wrapper type versus the driver layer, and the reason
Option A is the default recommendation.

## Notes

- With Option B, do **not** also register the driver wrapper for the same
  connection — each helper call would be analyzed twice.
- The wrapper's `Query`/`Exec` return the plain `*sql.Rows` / `sql.Result`
  types, exactly like sqlx's own.
