---
id: pgx
title: pgx / pgxpool
description: pgxguard — analyze native pgx and pgxpool queries through pgx's tracer seam, composing with otelpgx and other tracers already installed.
---

# pgx / pgxpool

The `database/sql` wrapper covers pgx's stdlib shim (`pgx/v5/stdlib`). The
**native** APIs — `pgx.Conn`, `pgxpool.Pool` — never touch `database/sql`,
so they need `pgxguard`. It hooks pgx's own `QueryTracer` and
`BatchTracer` seams, which is how every pgx ecosystem tool extends the
driver, so every `Query` / `QueryRow` / `Exec` and every `SendBatch` is
analyzed without a wrapper type or a method list.

```bash
go get github.com/KARTIKrocks/sqlguard/integrations/pgxguard
```

## With a pool

```go
import (
    "github.com/KARTIKrocks/sqlguard/integrations/pgxguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
    "github.com/jackc/pgx/v5/pgxpool"
)

cfg, err := pgxpool.ParseConfig(dsn)
pgxguard.ApplyPool(cfg,
    middleware.WithSlowQueryThreshold(50*time.Millisecond),
    middleware.WithN1Detection(10, time.Second),
)
pool, err := pgxpool.NewWithConfig(ctx, cfg)
```

## With a single connection

```go
cfg, err := pgx.ParseConfig(dsn)
pgxguard.Apply(cfg)
conn, err := pgx.ConnectConfig(ctx, cfg)
```

## API

| Symbol | Use |
| --- | --- |
| `pgxguard.ApplyPool(cfg *pgxpool.Config, opts ...middleware.Option) *pgxpool.Config` | Install on a pool config. Returns `cfg` for chaining. Panics on nil. |
| `pgxguard.Apply(cfg *pgx.ConnConfig, opts ...middleware.Option) *pgx.ConnConfig` | Install on a connection config. Returns `cfg`. Panics on nil. |
| `pgxguard.NewTracer(opts ...middleware.Option) *Tracer` | Build the tracer yourself, to keep a handle for `ResetN1()` or to compose manually. |
| `(*Tracer).ResetN1()` | Clear N+1 state at a request boundary. |

## Composes with existing tracers

pgx allows exactly one `Tracer` per config, and production services usually
already have one — `otelpgx`, `ddtrace`, a logger. `Apply` and `ApplyPool`
do not overwrite it. If `cfg.Tracer` is set, they wrap both in pgx's own
`multitracer`, which fans every event out to each tracer and routes by
interface, so the existing tracer keeps receiving exactly the events it
did before.

```go
cfg.ConnConfig.Tracer = otelpgx.NewTracer()
pgxguard.ApplyPool(cfg) // both tracers now run
```

## Keeping a handle for `ResetN1()`

`Apply` builds the tracer internally. When you need `ResetN1()`, build it
yourself and install it — with `multitracer` if you also have another
tracer:

```go
import "github.com/jackc/pgx/v5/multitracer"

tracer := pgxguard.NewTracer(middleware.WithN1Detection(10, time.Second))
cfg.ConnConfig.Tracer = multitracer.New(otelpgx.NewTracer(), tracer)

func handler(w http.ResponseWriter, r *http.Request) {
    defer tracer.ResetN1()
    // ...
}
```

## Coverage details

| pgx call | Seam | Static rules | N+1 | Latency |
| --- | --- | --- | --- | --- |
| `Query`, `QueryRow`, `Exec` | `QueryTracer` | yes | yes | yes, on success |
| Prepared statements executed via the above | `QueryTracer` | yes | yes | yes |
| `SendBatch` | `BatchTracer`, per statement | yes | yes | **no** |
| `CopyFrom` | — | no | no | no |

- **Batches**: pgx exposes only the whole-batch round trip, not
  per-statement latency, so `slow-query` is deliberately not reported for
  batch statements rather than reported wrongly.
- **`PrepareTracer` is intentionally not implemented**: execution already
  routes through `QueryTracer`, so tracing `Prepare` as well would
  double-report findings and inflate N+1 counts.
- **`CopyFrom`** carries no SQL statement and is out of scope.

The tracer is the reference implementation of the split start/end pattern
on [`middleware.Guard`](middleware#guard-for-integration-authors):
`TraceQueryStart` calls `Guard.Observe` and stashes the returned closure on
the context; `TraceQueryEnd` invokes it with `data.Err`.
