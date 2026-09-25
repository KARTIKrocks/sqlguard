---
id: gorm
title: GORM
description: gormguard — a gorm.Plugin that runs every GORM-generated statement through sqlguard.
---

# GORM

`gormguard` is a `gorm.Plugin`. Once registered, every statement GORM
renders — ORM calls and `db.Raw` / `db.Exec` alike — is analyzed by the
shared core.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/gormguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/gormguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
)

gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})

// Defaults: 200ms slow-query threshold, console reporter, no N+1.
gormguard.Register(gormDB)

// Or with options — the same middleware.Option set as everywhere else.
gormguard.Register(gormDB,
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)
```

## API

| Symbol | Use |
| --- | --- |
| `gormguard.Register(db *gorm.DB, opts ...middleware.Option) error` | Build the plugin and `db.Use` it in one call. |
| `gormguard.New(opts ...middleware.Option) *Plugin` | Build the plugin yourself, when you need to keep a handle for `ResetN1()`. |
| `(*Plugin).ResetN1()` | Clear N+1 state at a request boundary. No-op unless `WithN1Detection` was passed. |
| `(*Plugin).Name() string` | `"sqlguard"` — the `gorm.Plugin` name. |

```go
plugin := gormguard.New(middleware.WithN1Detection(10, time.Second))
gormDB.Use(plugin)

func handler(w http.ResponseWriter, r *http.Request) {
    defer plugin.ResetN1()
    // ...
}
```

## What is hooked

GORM v2 routes operations through six callback chains — **Create**,
**Query**, **Update**, **Delete**, **Row** (`db.Raw(…).Scan` / `.Row`)
and **Raw** (`db.Exec`). The plugin registers before/after callbacks on all
six. Hooking only the ORM chains would silently miss every `db.Raw` and
`db.Exec` in the codebase, which is exactly the SQL most worth analyzing.

GORM has not rendered `Statement.SQL` when the before-callback fires for
the ORM chains, so analysis happens in the after-callback with the final
SQL. Behaviour matches the driver wrapper: static rules run on every call;
latency is reported only when the statement succeeded.

## Notes

- GORM sits on `database/sql`, so `sqlguard.Register` on the underlying
  driver is an alternative that needs no plugin. Use this adapter when you
  want `ResetN1()`. Do not use both on one connection — each statement
  would be analyzed twice.
- Findings are [redacted](redaction) like everywhere else; GORM's own
  `Logger` is unaffected.
