---
id: ent
title: ent
description: entguard — decorate ent's dialect.Driver so every Exec, Query and transaction is analyzed, whatever opened the underlying *sql.DB.
---

# ent

ent runs on `database/sql`, so the simplest coverage is to point `entsql`
at a `*sql.DB` from `sqlguard.Register` / `OpenDB`. `entguard` is the
dedicated alternative: it decorates ent's own `dialect.Driver`, the same
seam ent's built-in `dialect.Debug` wrapper uses, so it works regardless of
how the `*sql.DB` was opened and gives you a handle for `ResetN1()`.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/entguard
```

```go
import (
    "entgo.io/ent/dialect"
    entsql "entgo.io/ent/dialect/sql"

    "github.com/KARTIKrocks/sqlguard/integrations/entguard"
    "github.com/KARTIKrocks/sqlguard/middleware"

    "myapp/ent"
)

drv, err := entsql.Open(dialect.Postgres, dsn)
guarded := entguard.Wrap(drv,
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)
client := ent.NewClient(ent.Driver(guarded))
```

## API

| Symbol | Use |
| --- | --- |
| `entguard.Wrap(d dialect.Driver, opts ...middleware.Option) *Driver` | Decorate any `dialect.Driver` — including one already wrapped by `dialect.Debug`. |
| `(*Driver).ResetN1()` | Clear N+1 state at a request boundary. |
| `Exec`, `Query`, `Tx`, `BeginTx` | The `dialect.Driver` methods; ent calls them. |

```go
guarded := entguard.Wrap(drv, middleware.WithN1Detection(10, time.Second))

func handler(w http.ResponseWriter, r *http.Request) {
    defer guarded.ResetN1()
    // ...
}
```

## What is covered

- `Exec` and `Query` on the driver.
- `Tx` and `BeginTx`, and every `Exec` / `Query` on the transactions they
  return. Statements inside a transaction are analyzed exactly like the
  ones outside it.

Every call flows through `middleware.Guard.Observe`: static rules run on
each statement, latency is reported only when it succeeded.

## Notes

- ent's generated client issues predictable, parameterized SQL, which is
  the case the [analysis cache](noise-control) was built for — after the
  first call each query shape is a cache hit.
- Do not combine `entguard` with a driver-level `sqlguard.Register` on the
  same `*sql.DB`; each statement would be analyzed twice.
