---
id: intro
title: Introduction
description: What sqlguard is, the three places it can analyze your SQL, and how the modules fit together.
slug: /
---

# Introduction

**sqlguard** is a production-safe SQL query analyzer for Go. It finds the
queries that hurt in production — `SELECT *`, a `DELETE` with no `WHERE`, an
N+1 loop, a `LIKE '%…'` that can never use an index, a query that took 800 ms
— and reports each one with a rule name, a redacted copy of the query, and a
suggested fix. Think of it as `golangci-lint` for the SQL your application
actually runs.

## Three entry surfaces, one analyzer

Every surface runs the same rules through the same analyzer and reporter, so
a finding looks the same whether it came from a test run, a CI job or a
production log.

| Surface | How | When to use it |
| --- | --- | --- |
| [Runtime middleware](./middleware.md) | Wraps the `database/sql` **driver**, returns a real `*sql.DB` | Catch everything your ORM or query builder emits, plus latency and N+1 |
| [Static scanner](./scan.md) | `sqlguard scan ./...` walks Go source and resolves string constants | Gate pull requests in CI without a database |
| [EXPLAIN analyzer](./explain.md) | `sqlguard explain --db …` plans a query on a live server | Check a specific query's plan for sequential scans and missing indexes |

Six [ORM and driver integrations](./integrations.md) — GORM, sqlx, native pgx,
bun, xorm and ent — are additional runtime surfaces. They hook each library's
own callback seam and route through the same exported
[`middleware.Guard`](./middleware.md#guard-for-integration-authors), so they inherit
every runtime behaviour without a second option surface to learn.

## What you get out of the box

- **21 detection rules** — 14 static SQL rules, `slow-query` and
  `n-plus-one` at runtime, and 5 plan-level rules from EXPLAIN. See
  [Rules](./rules.md).
- **Redaction by default** — string and numeric literals become `?` before a
  finding leaves the process, and every finding carries a stable, PII-free
  [fingerprint](./redaction.md) that is safe as a metric label.
- **Quiet in production** — repeated findings are
  [de-duplicated](./noise-control.md) per window, and repeated queries hit an
  exact-string cache instead of being re-parsed.
- **One config file** — a [`.sqlguard.yml`](./configuration.md) drives the
  middleware, the scanner and the CLI. [Inline suppressions](./suppressions.md)
  work without any config.
- **A pluggable parser** — a zero-dependency fallback by default, or an
  [opt-in real grammar](./parsers.md) for PostgreSQL or MySQL.

## Module layout

sqlguard is nine Go modules released in lockstep, so a heavy dependency never
enters your build unless you import the module that needs it:

| Module | Import path | Third-party deps |
| --- | --- | --- |
| Core | `github.com/KARTIKrocks/sqlguard` | none in `analyzer`, `middleware`, `reporter` |
| Config | `…/sqlguard/config` | `gopkg.in/yaml.v3` (only here) |
| CLI | `…/sqlguard/cmd/sqlguard` | cobra, `go/packages`, database drivers |
| Parsers | `…/sqlguard/parsers/pgparser`, `…/parsers/mysqlparser` | the respective SQL grammar |
| Integrations | `…/sqlguard/integrations/{gormguard,sqlxguard,pgxguard,bunguard,xormguard,entguard}` | the respective ORM/driver |

Importing `analyzer` or `middleware` pulls in nothing outside the standard
library.

## What sqlguard is not

- **Not a query rewriter.** It observes and reports; it never alters the SQL
  or the arguments on their way to the driver.
- **Not a schema linter.** Rules reason about the statement text and, via
  EXPLAIN, the plan — not your migrations.
- **Not a replacement for tracing.** The [pgx integration](./pgx.md) composes with
  `otelpgx` and friends rather than replacing them.

## Next steps

Start with [Getting Started](./getting-started.md) — it takes about a minute to
see the first finding.
