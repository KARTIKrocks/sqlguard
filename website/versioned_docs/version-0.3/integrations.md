---
id: integrations
title: Integrations Overview
description: Which ORM and driver adapters exist, what seam each one hooks, what it covers, and when the plain database/sql wrapper is enough.
---

# Integrations Overview

The [runtime middleware](middleware) wraps the `database/sql` driver, which
already covers every library built on `database/sql` — sqlc, ent, sqlx,
GORM, pgx-stdlib. The integrations exist for two reasons:

1. **The library bypasses `database/sql`.** Native pgx / pgxpool talk to
   PostgreSQL directly. [`pgxguard`](pgx) hooks pgx's tracer seam instead.
2. **You want a handle.** The driver wrapper hands back a bare `*sql.DB`,
   so there is nothing to call `ResetN1()` on. Every integration holds the
   guard and exposes `ResetN1()`, which lets you scope
   [N+1 detection](n-plus-one) to a request.

All six are built on the same exported `middleware.Guard`, so they inherit
redaction-by-default, fingerprints, the parser seam, config, de-duplication
and the analysis cache — and they all take the same
[`middleware.Option`](middleware#options) set. There is no second surface to
learn.

## Coverage matrix

| Module | Hooks | Covers | Install |
| --- | --- | --- | --- |
| [`gormguard`](gorm) | `gorm.Plugin` callbacks on all six chains | Create / Query / Update / Delete / Row / Raw | `go get github.com/KARTIKrocks/sqlguard/integrations/gormguard` |
| [`sqlxguard`](sqlx) | Wrapper around `*sqlx.DB` | `Select`, `Get`, `Queryx`, `NamedExec` (+ `Context`), `Query`, `Exec` | `go get github.com/KARTIKrocks/sqlguard/integrations/sqlxguard` |
| [`pgxguard`](pgx) | `pgx.QueryTracer` + `pgx.BatchTracer` | `Query`, `QueryRow`, `Exec`, `SendBatch` on `pgx.Conn` and `pgxpool.Pool` | `go get github.com/KARTIKrocks/sqlguard/integrations/pgxguard` |
| [`bunguard`](bun) | `bun.QueryHook` | Every query bun renders | `go get github.com/KARTIKrocks/sqlguard/integrations/bunguard` |
| [`xormguard`](xorm) | xorm `contexts.Hook` | Every statement xorm executes | `go get github.com/KARTIKrocks/sqlguard/integrations/xormguard` |
| [`entguard`](ent) | Decorates ent's `dialect.Driver` | `Exec`, `Query`, and transactions it opens | `go get github.com/KARTIKrocks/sqlguard/integrations/entguard` |

Each is its own Go module, so its ORM dependency enters your build only
when you import it. All nine modules in the repository are released in
lockstep with the same version number.

## Choosing

- **On `database/sql` and happy with process-wide N+1 windows?** Use
  `sqlguard.Register` / `OpenDB` and nothing else. It sees more than any
  adapter — every statement, including the ones an ORM issues that its
  hook seam does not surface.
- **On native pgx / pgxpool?** `pgxguard`. There is no alternative; the
  driver wrapper never sees those queries.
- **Need `ResetN1()` per request on an ORM?** The matching adapter.
- **On sqlx and want everything covered?** Layer sqlx over the driver
  wrapper (`sqlx.NewDb(sqlDB, "postgres")`) — it covers every sqlx method.
  `sqlxguard` wraps the common helpers and gives you `ResetN1()`.

Using two at once — the driver wrapper _and_ an ORM adapter on the same
connection — analyzes each statement twice. Pick one per connection.

## Writing your own

Any library with a before/after hook (or a single "here is the SQL" hook)
can be integrated in a few dozen lines on top of `middleware.NewGuard`.
`pgxguard` is the reference for split start/end hooks (`Guard.Observe`),
`gormguard` for a seam that only exposes SQL after execution
(`Guard.Check` + `Guard.CheckLatency`). See
[`Guard` for integration authors](middleware#guard-for-integration-authors).
