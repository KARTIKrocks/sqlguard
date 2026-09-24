---
id: explain
title: EXPLAIN Analyzer
description: sqlguard explain — plan a query against live PostgreSQL, MySQL or MariaDB, flag sequential scans and missing indexes, and never execute it.
---

# EXPLAIN Analyzer

Static rules can tell you a query _looks_ risky. Only the database can tell
you it _is_: that the `WHERE` you wrote hits no index, that the planner
chose a sequential scan over ten million rows, that the `ORDER BY` needs a
filesort. `sqlguard explain` asks the planner and reports what it finds —
without ever executing the query.

## Usage

```bash
# PostgreSQL
sqlguard explain --db "postgres://app:secret@localhost/app?sslmode=disable" \
    "SELECT id, total FROM orders WHERE customer_id = 42"

# MySQL / MariaDB
sqlguard explain --dialect mysql --db "app:secret@tcp(localhost:3306)/app" \
    "SELECT id, total FROM orders WHERE customer_id = 42"

# JSON for tooling
sqlguard explain --db "…" --format json "SELECT …"
```

| Flag | Default | Effect |
| --- | --- | --- |
| `--db <dsn>` | required | Connection string. Postgres DSNs use pgx's `postgres://` URL or key=value form; MySQL uses `go-sql-driver/mysql` DSN syntax. |
| `--dialect postgres\|mysql` | `postgres` | Which planner to talk to. MariaDB works through `mysql`. |
| `--format console\|json` | `console` | Output shape. |
| `--allow-dml` | off | Permit `INSERT` / `UPDATE` / `DELETE`. Still planned only, still rolled back. |
| `--config`, `--no-config` | — | Persistent flags; `explain` findings are not affected by `rules:` config. |

The whole command runs under a 30-second timeout, including the initial
connectivity check. Exit code is **1** when the plan has issues, **0**
when clean.

As with [`scan`](scan#usage), the console rendering goes to stderr and
`--format json` goes to stdout, always as an array. _Changed in 0.3._ In 0.2
JSON went to stderr, so redirecting it produced an empty file.

```text
[SQLGUARD WARNING] seq-scan
  Query: SELECT id, total FROM orders WHERE customer_id = 42
  Issue: Sequential scan detected (estimated 812430 rows, cost 21877.5)
  Fix:   Consider adding an index to avoid full table scan.

[SQLGUARD WARNING] high-cost
  Query: SELECT id, total FROM orders WHERE customer_id = 42
  Issue: High cost operation: Seq Scan (cost 21877.5)
  Fix:   Review query plan and consider optimization.

2 issue(s) found in query plan
```

Unlike every other surface, `Query` here is the raw text you typed —
there is no log sink to protect, and you need to recognise your own query.
`Fingerprint` is still set. See [Redaction](redaction#the-one-deliberate-exception).

## What it detects

| Rule | Dialect | Fires on |
| --- | --- | --- |
| `seq-scan` | postgres | A `Seq Scan` node. `INFO` at ≤ 1,000 estimated rows, `WARNING` above. |
| `high-cost` | postgres | Any node with `Total Cost` > 10,000. |
| `full-table-scan` | mysql | A row with access `type = ALL`. |
| `no-index-used` | mysql | A row with empty `key` **and** empty `possible_keys`. |
| `filesort` | mysql | `Using filesort` in `Extra`. |

Postgres plans are requested as `EXPLAIN (FORMAT JSON)` and walked
recursively, so nested scans inside joins and CTEs are found. MySQL plans
are requested as `EXPLAIN FORMAT=TRADITIONAL` — MySQL 9 defaults
`@@explain_format` to `TREE`, which is a single free-text column — and read
**by column name**, because MariaDB emits 10 columns where MySQL emits 12.
`UNION RESULT` and derived-table rows (`<union1,2>`, `<derived2>`) are
skipped: they name temporary tables that have no index by construction.

If the server returns a plan shape the analyzer does not recognise — a
missing expected column, say — it **fails with an error** rather than
reporting "no issues". A plan checker that says clean when it could not
look is worse than one that says so.

## Safety model

`EXPLAIN` cannot take bind parameters, so the query text is necessarily
concatenated into the `EXPLAIN` statement. The defense is layered and does
not rely on parameterization:

1. **Validation.** Empty input is refused. Multi-statement input is refused
   using a comment- and string-literal-aware check
   (`analyzer.IsMultiStatement`), so a `;` hidden in a `--` comment, a
   `/* */` block or a string cannot smuggle a second statement. That check
   takes the _narrowest_ reading of a string literal — the opposite of
   [`Redact`](redaction#when-the-dialect-is-ambiguous-it-over-redacts), which
   takes the widest. The two have opposite fail-safe directions: redaction
   must never leave a literal byte in its output, while this check must never
   miss a separator, so `'a\'; DROP TABLE t; --'` is refused rather than read
   as one literal. The statement is then classified with the fallback parser:
   `SELECT` / `WITH` pass; `INSERT` / `UPDATE` / `DELETE` pass only with
   `--allow-dml`; DDL, `SET`, transaction control and anything unrecognised
   are always refused.

   _Changed in 0.3._ The separator check previously scanned with a single
   reading of `$$`, which let some stacked input through. It now counts a `;`
   as a separator whenever _any_ dialect reading leaves it outside a literal,
   because each reading of `$$` is blind to a different payload. Scanning
   `$tag$…$tag$` as a literal is what hides the `;` in
   `UPDATE t AS $$ SET id = 1; DROP TABLE t` — on MySQL those are identifier
   bytes, but that reading opens an unterminated dollar body which swallows
   the rest. Leaving `$$` as ordinary bytes is what hides the `;` in
   `SELECT $$'$$; DROP TABLE t` — on PostgreSQL the apostrophe is data inside
   the dollar body, but that reading opens an unterminated ordinary literal
   which swallows the rest. Each catches what the other misses, so both run.
   The cost is that a single statement carrying a `;` inside a dollar-quoted
   body, such as `SELECT $$a ; b$$`, is refused; a one-statement query
   occasionally rejected is the safe error here.
2. **A transaction that is always rolled back.** Every `EXPLAIN` runs
   inside `BeginTx` with a deferred `Rollback`. Nothing commits.
3. **Never `ANALYZE`.** `EXPLAIN ANALYZE` executes the statement to collect
   real timings; sqlguard never uses it. The statement is planned, only.

The transaction is additionally opened **read-only** everywhere except
MySQL/MariaDB under `--allow-dml`. Those servers reject _every_ statement
in a `READ ONLY` transaction with error 1792 — including an `EXPLAIN` that
only plans a DML statement — so there the guarantee rests on the three
layers above. This is deliberate; it is not a gap.

## Library use

The CLI is a thin wrapper over the `explain` package, which works with any
`*sql.DB`:

```go
import "github.com/KARTIKrocks/sqlguard/explain"

pa, err := explain.New(db, "postgres")              // or "mysql"
pa, err = explain.New(db, "mysql", explain.WithAllowDML())

res, err := pa.Analyze(ctx, "SELECT id FROM orders WHERE customer_id = 42")
for _, issue := range res.Issues {                 // []analyzer.Result
    fmt.Println(issue.RuleName, issue.Message)
}
fmt.Println(res.RawPlan)                           // the plan text, for humans
```

`explain.Result` carries `Query`, `RawPlan` and `Issues`. Pair it with a
test that runs your hottest queries through `Analyze` against a seeded
database — a `seq-scan` on the orders table is cheaper to find in CI than
at 3 a.m.

## Drivers

_Changed in 0.3._ The CLI links `github.com/jackc/pgx/v5/stdlib` and
`github.com/go-sql-driver/mysql`, so `sqlguard explain` works out of the
box. Only the `cmd/sqlguard` package imports them; a library consumer of
`analyzer` or `middleware` never compiles them in. In 0.2 the released
binary shipped without a driver and `explain` could not connect.
