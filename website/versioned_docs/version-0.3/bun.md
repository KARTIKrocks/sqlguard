---
id: bun
title: bun
description: bunguard — a bun.QueryHook that analyzes every statement bun renders.
---

# bun

`bunguard` is a `bun.QueryHook`. bun renders SQL through its own query
builder and exposes the final text and a start timestamp in `AfterQuery`,
which is where the hook runs the static rules and the latency check.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/bunguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/bunguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "github.com/uptrace/bun"
    "github.com/uptrace/bun/dialect/pgdialect"
    "github.com/uptrace/bun/driver/pgdriver"
)

sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
db := bun.NewDB(sqldb, pgdialect.New())

db.AddQueryHook(bunguard.New(
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
))
```

## API

| Symbol | Use |
| --- | --- |
| `bunguard.New(opts ...middleware.Option) *QueryHook` | Build the hook. Pass to `db.AddQueryHook`. |
| `(*QueryHook).ResetN1()` | Clear N+1 state at a request boundary. |
| `BeforeQuery`, `AfterQuery` | The `bun.QueryHook` methods; you do not call them. |

```go
hook := bunguard.New(middleware.WithN1Detection(10, time.Second))
db.AddQueryHook(hook)

func handler(w http.ResponseWriter, r *http.Request) {
    defer hook.ResetN1()
    // ...
}
```

## Notes

- Static rules run on every query; latency is reported only when the
  query succeeded (`event.Err == nil`). Same semantics as every other
  surface.
- bun's `pgdriver` is not a `database/sql` driver name you can wrap with
  `sqlguard.Register`, but `sqlguard.OpenDB(pgdriver.NewConnector(...))`
  works if you prefer driver-level coverage over `ResetN1()`. Do not use
  both on one connection.
