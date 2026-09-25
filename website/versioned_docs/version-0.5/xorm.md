---
id: xorm
title: xorm
description: xormguard — an xorm contexts.Hook that analyzes every statement the engine executes.
---

# xorm

`xormguard` implements xorm's `contexts.Hook`. xorm's `AfterProcess` hook
exposes the rendered SQL and the measured execution time, which is where
the static rules and the latency check run.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/xormguard
```

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/xormguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "xorm.io/xorm"
)

engine, err := xorm.NewEngine("pgx", dsn)

engine.AddHook(xormguard.New(
    middleware.WithSlowQueryThreshold(500*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
))
```

## API

| Symbol | Use |
| --- | --- |
| `xormguard.New(opts ...middleware.Option) *Hook` | Build the hook. Pass to `engine.AddHook`. |
| `(*Hook).ResetN1()` | Clear N+1 state at a request boundary. |
| `BeforeProcess`, `AfterProcess` | The `contexts.Hook` methods; you do not call them. |

```go
hook := xormguard.New(middleware.WithN1Detection(10, time.Second))
engine.AddHook(hook)

func handler(w http.ResponseWriter, r *http.Request) {
    defer hook.ResetN1()
    // ...
}
```

## Notes

- Static rules run on every statement; latency is reported only on
  success. Same semantics as every other surface.
- xorm sits on `database/sql`, so `sqlguard.Register` on the driver you
  pass to `xorm.NewEngine` is the alternative. Use this adapter when you
  want `ResetN1()`; do not use both on one engine.
