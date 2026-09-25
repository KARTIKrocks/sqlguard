---
id: getting-started
title: Getting Started
description: Install sqlguard, wrap a database/sql driver, and see the first finding in under a minute.
---

# Getting Started

## Installation

Requires **Go 1.27+**. _Changed in 0.2._ Previously Go 1.26+.

```bash
go get github.com/KARTIKrocks/sqlguard
```

The CLI (static scanner and EXPLAIN analyzer) is a separate binary:

```bash
go install github.com/KARTIKrocks/sqlguard/cmd/sqlguard@latest
```

To pin a release:

```bash
go get github.com/KARTIKrocks/sqlguard@v0.2.0
```

## Runtime: wrap a driver

sqlguard wraps at the `database/sql` **driver** layer. You register a new
driver name that delegates to an existing one, open it as usual, and get a
plain `*sql.DB` back — nothing else in your code changes.

```go
package main

import (
    "database/sql"
    "log"
    "time"

    _ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver

    "github.com/KARTIKrocks/sqlguard"
    "github.com/KARTIKrocks/sqlguard/middleware"
)

func main() {
    // Wrap the "pgx" driver under a new name.
    if err := sqlguard.Register("sqlguard-pg", "pgx",
        middleware.WithSlowQueryThreshold(500*time.Millisecond),
        middleware.WithN1Detection(5, 2*time.Second),
    ); err != nil {
        log.Fatal(err)
    }

    db, err := sql.Open("sqlguard-pg", "postgres://app@localhost/app")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Use db exactly as before. Every query is analyzed on its way through.
    rows, err := db.Query("SELECT * FROM users WHERE email = 'a@b.c'")
    // ...
}
```

The query above produces one finding on stderr:

```text
[SQLGUARD WARNING] select-star
  Query: SELECT * FROM users WHERE email = ?
  Issue: SELECT * detected. Selecting all columns can hurt performance.
  Fix:   Select only the columns you need.
```

Note the literal `'a@b.c'` became `?` — findings are
[redacted by default](redaction) so customer data never lands in a log.

If you already hold a `driver.Connector` (for example from pgx's
`stdlib.GetConnector`), skip the registry:

```go
db := sqlguard.OpenDB(connector, middleware.WithN1Detection(5, time.Second))
```

Using an ORM? The driver wrapper already covers anything built on
`database/sql` — sqlc, ent, sqlx, GORM, pgx-stdlib. For native pgx/pgxpool
and for ORM-specific seams, see [Integrations](integrations).

## Static: scan your source

```bash
sqlguard scan ./...
```

The scanner finds calls like `db.Query(...)`, `tx.ExecContext(...)` and
`stmt.QueryRow(...)`, resolves the SQL from literals, constants and
`fmt.Sprintf` with a constant format, and runs the same static rules:

```text
[SQLGUARD CRITICAL] delete-without-where
  File: internal/repo/users.go:42
  Query: DELETE FROM sessions
  Issue: DELETE without WHERE clause detected. This will delete all rows.
  Fix:   Add a WHERE clause to limit the scope of the delete.

1 issue(s) found (17 file(s) scanned)
```

It exits **1** when it finds anything and **0** when clean, so it drops into
a CI step as-is. `--format json` emits machine-readable output. See
[Static scanner](scan).

## Plan: EXPLAIN a query

```bash
sqlguard explain --db "postgres://app@localhost/app?sslmode=disable" \
    "SELECT id FROM orders WHERE customer_id = 42"
```

The query is planned — never executed — inside a read-only transaction that
is always rolled back, and the plan is checked for sequential scans, missing
indexes, filesorts and high-cost nodes. See [EXPLAIN analyzer](explain).

## Configure once

Drop a [`.sqlguard.yml`](configuration) at the repository root and every
surface picks it up. Feed the same file to the middleware with one line:

```go
opts, err := config.Middleware("", ".") // discover from the working directory
sqlguard.Register("sqlguard-pg", "pgx", opts...)
```

Or suppress a single query inline, no config needed:

```sql
SELECT * FROM feature_flags -- sqlguard:ignore:select-star
```

## Where next

- [Runtime middleware](middleware) — every option, and what is intercepted.
- [Rules](rules) — what each of the 21 rules catches and why it matters.
- [Integrations](integrations) — GORM, sqlx, pgx, bun, xorm, ent.
