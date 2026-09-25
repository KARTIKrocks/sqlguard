---
id: rules
title: Detection Rules
description: Reference for all 21 rules — what triggers each one, why it matters, the suggested fix, default severity, and the tunables.
---

# Detection Rules

Every finding names a rule. The name is stable: it is what you disable,
re-prioritise or tune in [`.sqlguard.yml`](configuration), and what you
put after `sqlguard:ignore:` in a [suppression](suppressions).

Severity is one of `INFO`, `WARNING`, `CRITICAL`, and every rule's default
can be overridden per project.

## At a glance

| Rule | Severity | Where | Fires on |
| --- | --- | --- | --- |
| `select-star` | WARNING | static, runtime | `SELECT *` / `SELECT t.*` |
| `leading-wildcard` | WARNING | static, runtime | `LIKE '%…'`, `ILIKE '%…'` |
| `non-sargable-predicate` | WARNING | static, runtime | `WHERE LOWER(col) = …`, `WHERE col::text = …` |
| `add-not-null-without-default` | WARNING | static, runtime | `ALTER TABLE … ADD COLUMN … NOT NULL` with no `DEFAULT` |
| `implicit-join` | WARNING | static, runtime | `FROM a, b` |
| `cartesian-join` | WARNING | static, runtime | Multi-table `FROM` with no join condition and no `WHERE` |
| `in-list-too-large` | WARNING | static, runtime | `IN (…)` with more than `max-length` elements (default 100) |
| `large-offset` | WARNING | static, runtime | Literal `OFFSET` above `threshold` (default 1000) |
| `select-distinct` | INFO | static, runtime | `SELECT DISTINCT` |
| `delete-without-where` | CRITICAL | static, runtime | `DELETE` with no `WHERE` |
| `update-without-where` | CRITICAL | static, runtime | `UPDATE` with no `WHERE` |
| `insert-without-columns` | WARNING | static, runtime | `INSERT INTO t VALUES (…)` with no column list |
| `select-without-limit` | WARNING | static, runtime | `SELECT … FROM` with neither `LIMIT` nor `WHERE` |
| `orderby-without-limit` | INFO | static, runtime | `ORDER BY` with no `LIMIT` |
| `n-plus-one` | WARNING | runtime | Same fingerprint `threshold` times within `window` |
| `slow-query` | WARNING | runtime | Latency at or above the threshold (default 200 ms) |
| `seq-scan` | INFO / WARNING | EXPLAIN (postgres) | A `Seq Scan` node; WARNING above 1,000 estimated rows |
| `high-cost` | WARNING | EXPLAIN (postgres) | Any plan node with total cost above 10,000 |
| `full-table-scan` | WARNING | EXPLAIN (mysql) | Access `type = ALL` |
| `no-index-used` | WARNING | EXPLAIN (mysql) | Empty `key` **and** empty `possible_keys` |
| `filesort` | INFO | EXPLAIN (mysql) | `Using filesort` in `Extra` |

_Changed in 0.3._ Every rule in this table is addressable by name in
[`.sqlguard.yml`](configuration): `disable` and `severity` work the same for a
runtime or plan rule as for a statement rule. In 0.2 only the 14 statement
rules were — naming any of the other seven warned with `unknown rule`, and
failed under `strict: true`.

Two qualifications. `only:` is a whitelist over the rules evaluated against a
statement, so it reaches neither the runtime findings nor the plan rules —
see [what `only:` reaches](configuration#what-only-reaches). And `settings` only
exists where a rule has a tunable: `leading-wildcard`, `in-list-too-large`,
`large-offset`, `slow-query` and `n-plus-one` have them; the five plan rules
have none and their thresholds are fixed, so a `settings` block for one is
reported as having no effect.

The runtime and plan rules are not evaluated against parsed SQL — middleware
derives them from latency and fingerprint counts, and `explain` from the
database's own plan — so they never fire during a static `sqlguard scan`.

"static, runtime" rules read the normalized `Statement` a [parser](parsers)
produces; they never look at raw SQL. The runtime and EXPLAIN rules are
built into the [middleware](middleware) and the [EXPLAIN analyzer](explain)
respectively and are not part of `analyzer.Default()`.

## Static rules

### `select-star`

`SELECT *` couples the query to the table's current column list: a new wide
column makes every caller slower, a dropped column breaks scans into
structs, and the database cannot serve the query from a covering index.
Aggregate forms such as `COUNT(*)` are not flagged.

> **Fix:** Select only the columns you need.

### `leading-wildcard`

`LIKE '%foo'` and `LIKE '%foo%'` (and Postgres `ILIKE`) cannot use a B-tree
index — the planner has nothing to seek to — so they scan the whole table.

> **Fix:** Use prefix search or a full-text index.

**Setting `min-length`** (default `0`, off): ignore patterns whose
searchable term — the literal with its surrounding `%` trimmed — is shorter
than this. `LIKE '%x%'` on a small lookup table is often intentional.

```yaml
rules:
  settings:
    leading-wildcard:
      min-length: 3
```

When the term length is unknown (a real parser that did not compute it),
the rule fires rather than staying silent — an unknown is never treated as
"short".

### `non-sargable-predicate`

A function or cast applied to a column on the column side of a comparison —
`WHERE LOWER(email) = $1`, `WHERE created_at::date = $1` — means an ordinary
index on that column cannot be used.

> **Fix:** Compare the bare column instead, or add a matching expression/function index.

### `add-not-null-without-default`

`ALTER TABLE t ADD COLUMN c int NOT NULL` fails on a populated table, or
forces a full rewrite with an implicit default, depending on the engine.

> **Fix:** Add a `DEFAULT`, or split into: add the column nullable, backfill, then `SET NOT NULL`.

### `implicit-join`

`FROM orders, customers` is the old comma join. It works, until the join
condition in `WHERE` is forgotten or dropped — and then it is a cartesian
product that returns rows × rows.

> **Fix:** Use explicit `JOIN … ON` syntax.

### `cartesian-join`

The high-confidence subset of the above: multiple tables (comma join,
`CROSS JOIN`, or a bare `JOIN`) with **no** `ON` / `USING` / `NATURAL` and
**no** top-level `WHERE`. Both rules can fire on the same statement.

> **Fix:** Add a `JOIN … ON` condition (or a `WHERE` clause relating the tables).

### `in-list-too-large`

A very long `IN (…)` value list is slow to plan, blows past prepared-
statement parameter limits on some drivers, and usually means a set that
should have been a join. `IN (SELECT …)` subqueries are never counted.

> **Fix:** Use a `JOIN` against a temp table / `VALUES` list, or a parameterized array such as `= ANY($1)`.

**Setting `max-length`** (default `100`): flag lists with more than this
many elements.

### `large-offset`

`OFFSET 100000` makes the database produce and discard 100,000 rows before
returning any. Page 1 is fast; page 1,000 is not. Parameterized offsets
(`OFFSET $1`) cannot be evaluated statically and are never flagged; MySQL's
`LIMIT offset, count` form is recognised.

> **Fix:** Use keyset (cursor) pagination: `WHERE id > $last ORDER BY id LIMIT n`.

**Setting `threshold`** (default `1000`): flag a literal offset above this.

### `select-distinct`

`SELECT DISTINCT` is frequently a patch over a join that fans out — the
duplicates are the bug, and `DISTINCT` hides it while adding a sort. This
is `INFO` by default because it is sometimes exactly right. Postgres
`DISTINCT ON` and MySQL `DISTINCTROW` count; `COUNT(DISTINCT col)` does not.

> **Fix:** Confirm the duplicates aren't a join fan-out; prefer fixing the join or using `EXISTS` / `GROUP BY`.

### `delete-without-where`

A `DELETE` with no `WHERE` deletes every row. There is almost no situation
in application code where that is intended, which is why it is `CRITICAL`.
For the rare case that it is, [suppress it](suppressions) at the call site.

> **Fix:** Add a `WHERE` clause to limit the scope of the delete.

### `update-without-where`

Same as above for `UPDATE`.

> **Fix:** Add a `WHERE` clause to limit the scope of the update.

### `insert-without-columns`

`INSERT INTO t VALUES (…)` and `INSERT INTO t SELECT …` bind positionally
to the table's column order. Adding, dropping or reordering a column
silently shifts every value.

_Changed in 0.5._ Every keyword that inserts rows this way is flagged, not
just `INSERT`: MySQL/SQLite's `REPLACE`, the `UPSERT` CockroachDB accepts,
and the forms that omit the optional `INTO` (`INSERT t VALUES (…)`).
Previously only a leading `INSERT INTO` was recognised, so the others were
reported solely to users who had opted into a dialect parser.

Forms that name their columns, or have none to name, are not flagged:
MySQL's `INSERT INTO t SET col = …`, and PostgreSQL's
`INSERT INTO t DEFAULT VALUES`, which writes no caller-supplied values at
all.

> **Fix:** Specify columns explicitly: `INSERT INTO table (col1, col2) VALUES (…)`.

### `select-without-limit`

A `SELECT … FROM` with neither a `WHERE` filter nor a `LIMIT` returns the
whole table. Fine for a ten-row config table; not for one that grows.
`SELECT 1` and `SELECT version()` (no `FROM`) are not flagged.

> **Fix:** Add a `LIMIT` clause or `WHERE` filter to restrict results.

### `orderby-without-limit`

`ORDER BY` with no `LIMIT` sorts the entire result set even if the caller
only reads the first few rows. `INFO` by default: a full sorted export is a
legitimate query.

> **Fix:** Add a `LIMIT` clause if you only need a subset of rows.

## Runtime rules

### `n-plus-one`

Emitted by the middleware when the same query fingerprint executes
`threshold` times inside `window`. Off unless it is switched on — with
`WithN1Detection` in Go, or by setting both
`rules.settings.n-plus-one.threshold` and `.window` in
[config](configuration) _0.3+_. Full description in
[N+1 detection](n-plus-one).

> **Fix:** Consider using a `JOIN` or `IN` clause to batch these queries.

### `slow-query`

Emitted when a successful query's latency, measured at the driver, reaches
the threshold (`WithSlowQueryThreshold`, default 200 ms; or
`rules.settings.slow-query.threshold` in config — _changed in 0.3_, this was
a top-level `slow-query.threshold` key). The message includes the measured time
and the threshold. Reported on every slow execution — it is not
[de-duplicated](noise-control).

> **Fix:** Consider adding indexes or optimizing the query.

## EXPLAIN rules

Produced by [`sqlguard explain`](explain) from the query plan. _Changed in
0.3._ These are configurable through `rules:` in `.sqlguard.yml` like any
other rule; in 0.2 they were not, and naming one was an `unknown rule`
warning.

### `seq-scan` (PostgreSQL)

Every `Seq Scan` node in the plan. `INFO` when the planner estimates 1,000
rows or fewer (a small table scan is often the right plan), `WARNING`
above that. The message carries the estimated rows and the node's total
cost.

### `high-cost` (PostgreSQL)

Any node whose `Total Cost` exceeds 10,000 planner units, named by node
type.

### `full-table-scan` (MySQL / MariaDB)

A plan row with access `type = ALL`, with the estimated row count.

### `no-index-used` (MySQL / MariaDB)

A plan row where both `key` and `possible_keys` are empty — the optimizer
did not even have a candidate index.

### `filesort` (MySQL / MariaDB)

`Using filesort` in the `Extra` column: the `ORDER BY` is not covered by an
index and is being sorted after the fact.

## Tuning

Everything above can be disabled, re-prioritised or tuned per project in
[`.sqlguard.yml`](configuration), or silenced at a single site with a
[suppression](suppressions). To add rules of your own, see
[Analyzer API](analyzer).
