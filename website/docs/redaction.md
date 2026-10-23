---
id: redaction
title: Redaction & Fingerprints
description: Why findings never carry literal values, how Redact and Fingerprint work, what a fingerprint is safe for, and how to opt out for local debugging.
---

# Redaction & Fingerprints

sqlguard's findings flow into logs. A query like
`SELECT * FROM users WHERE email = 'ceo@example.com'` is exactly the query
most likely to trip a rule, and the literal in it is exactly what must not
reach a log sink. So by default **no `Result` leaves the process with a
literal value in it**. This is a security invariant, not a formatting
choice — see the repository's `SECURITY.md`.

## What redaction does

`analyzer.Redact` is a zero-dependency lexical pass over the SQL:

- Comments (`--` and `/* */`) are stripped.
- Every single-quoted string literal becomes `?`, honoring both `''` and
  `\'` escapes.
- Every dollar-quoted string (`$$…$$`, `$tag$…$tag$`) becomes `?`. `$1` /
  `$2` bind placeholders are left alone.
- Every numeric literal becomes `?`, including hex (`0x1F`) and binary
  (`0b1011`) forms.
- Keywords, structure, and identifiers — including `"double-quoted"` and
  `` `backtick` `` names — are preserved.

It never errors; unparseable input still comes out with its literals gone.

_Changed in 0.3._ Only the `''` escape was honored before, so a literal
containing `\'` closed at the wrong quote and the scanner then copied the
_following_ literal's contents out as query structure. Numeric redaction
stopped at the radix prefix, leaving `0x4142` as `?x4142` — the payload
intact. _Added in 0.3._ Dollar-quoted strings were not recognised at all and
passed through whole.

```text
in:  SELECT * FROM "users" WHERE email = 'a@b.c' AND age > 30 -- vip
out: SELECT * FROM "users" WHERE email = ? AND age > ?
```

### When the dialect is ambiguous, it over-redacts

Dialects disagree about whether a backslash escapes a quote. MySQL's default
`sql_mode` and PostgreSQL's `E'…'` strings say yes; PostgreSQL with
`standard_conforming_strings` says the backslash is an ordinary byte. The two
readings disagree about _where a literal ends_, and picking the wrong one
desynchronises the lexer — it closes a literal at the wrong quote and then
emits the next literal's contents as if they were query structure.

So `Redact` scans under both readings and redacts the union. A byte that is
inside a literal under _either_ reading is replaced:

```text
in:  SELECT * FROM t WHERE path = 'C:\' AND secret = 'hunter2'
out: SELECT * FROM t WHERE path = ?
```

That output has lost the `AND secret =` structure, which is the deliberate
trade: losing structure costs readability, and under-redacting leaks a value.
SQL with no backslash in it is unambiguous, scans identically under both
readings, and is unaffected.

One consequence is worth knowing about, because it reaches past readability:
an over-redacted query has a **different fingerprint** from the same query
with an unambiguous value. Above, `p = 'C:\'` folds to
`… WHERE p = ?` while `p = 'plain'` folds to `… WHERE p = ? AND id = ?`. So a
query whose values only _sometimes_ end in a backslash — Windows paths,
`LIKE … ESCAPE` patterns, regexes — groups under two fingerprints instead of
one. That splits [N+1](n-plus-one) counts across both (a run that would trip
a threshold of 20 may land as 12 and 8 and trip neither) and lets the
[de-duplicator](noise-control) emit the same finding once per group. Bind
parameters avoid it entirely, and are the better answer for those values
anyway.

### Double quotes depend on the dialect

A `"double-quoted"` run is an identifier in PostgreSQL, ANSI SQL and MySQL
under `ANSI_QUOTES`, but MySQL's default `sql_mode` also reads it as a _string
literal_. So what sqlguard does with one depends on the
[dialect](#the-dialect-hint):

| Dialect | `"users"` | `"alice@example.com"` |
| --- | --- | --- |
| PostgreSQL | kept | kept (it can only be a name) |
| MySQL | `?` | `?` |
| unknown | kept | `?` |

_Changed in 0.6._ Before, every double-quoted run was kept, so on MySQL a
value written as `"…"` reached the log. With the dialect unknown, a run is now
kept only when it looks like an identifier: ASCII letters, digits, `_` and `$`,
not starting with a digit. Anything else is redacted, which costs readability
on an unusual quoted name such as `"Order Details"`. A value that itself looks
like a name is still kept with the dialect unknown: `"alice"`, but also a
token such as `"sk_live_51Habc"`. On MySQL, set the dialect, or use `'…'` or
bind parameters for values. An unterminated `"` is redacted to the end of the
query in every dialect.
Backticks are always identifiers.

Under MySQL with `ANSI_QUOTES`, `"…"` is an identifier, so a MySQL analyzer
over-redacts names there. That is the safe direction.

### The dialect hint

_Added in 0.6._ The gaps above exist because the lexer does not know the
dialect unless told. An analyzer carries one (`analyzer.Dialect`: unknown,
`postgres` or `mysql`), resolved once when it is built. It decides the double
quotes above; the backslash case is not dialect-aware yet.

You rarely set it yourself. In order, the first of these that names a
dialect wins:

1. `middleware.WithDialect(d)` in Go.
2. `dialect:` in [`.sqlguard.yml`](configuration).
3. A single-dialect parser: `pgparser` or `mysqlparser`.
4. Detection: the `pgx`, `lib/pq` and `go-sql-driver/mysql` drivers behind
   `Register`, `OpenDB`, `WrapDriver` and `WrapConnector`; `pgxguard`
   (always Postgres); `sqlxguard` and `entguard` from the driver name; and
   `gormguard` from the dialector. `bunguard` and `xormguard` cannot see the
   database when they are built, so pass `WithDialect` there.

A driver behind another wrapper, or SQLite, stays unknown, which keeps the
dialect-agnostic behaviour described above.

`Result.Query` is set from `Redact` centrally in `Analyzer.Analyze`, and a
`slow-query` finding goes through `Analyzer.PrepareQuery`, which applies the
same policy. An `n-plus-one` finding describes a pattern rather than one
execution, so its `Query` is the fingerprint. There is one normalizer in the
codebase; nothing re-implements it.

## Fingerprints

Every `Result` also carries `Fingerprint`: the redacted query with
whitespace collapsed, `IN (?, ?, ?)` and `VALUES (?, ?)` lists folded to
`(?)`, and a trailing `;` trimmed.

```text
SELECT id FROM t WHERE x IN (1, 2, 3)   →  SELECT id FROM t WHERE x IN (?)
SELECT id FROM t WHERE x IN (4, 5)      →  SELECT id FROM t WHERE x IN (?)
SELECT id\n  FROM t\n  WHERE x = 'a';   →  SELECT id FROM t WHERE x = ?
```

A fingerprint is:

- **Stable** — the same query shape always yields the same string.
- **PII-free** — it is derived from the redacted form.
- **Low-cardinality** — literals and list lengths do not create new values.

That makes it safe as a metrics label, a log key, or a grouping key in your
observability stack. It is the value the [N+1 tracker](n-plus-one) groups
on and the [de-duplicator](noise-control) keys on. The JSON reporter emits
it as `fingerprint`. To compute one yourself, call `Fingerprint(sql)` on the
configured analyzer (`Guard.Analyzer()`), which reads `"…"` in its
[dialect](#the-dialect-hint); the package-level `analyzer.Fingerprint(sql)`
always uses the unknown dialect, so on MySQL the two can differ.

`Fingerprint` is **always** populated, whether or not redaction is on.

## Opting out

Only where the query text is trusted — local debugging, a test — you can
keep the raw SQL in `Result.Query`:

```go
a := analyzer.Default().WithRawQuery()
sqlguard.Register("sqlguard-pg", "pgx", middleware.WithAnalyzer(a))
```

or in [`.sqlguard.yml`](configuration):

```yaml
redact: false
```

`WithRawQuery` returns a copy; the original analyzer is unchanged. Do not
ship this to production.

## The one deliberate exception

The [EXPLAIN analyzer](explain) keeps `Result.Query` raw. The user typed the
query on their own command line; it goes back to their own terminal and
never reaches a log or telemetry sink. `Fingerprint` is still set. The
exception is scoped to `explain` only.

## Using it in your own reporter

```go
type metricsReporter struct{ counter *prometheus.CounterVec }

func (m *metricsReporter) Report(rs []analyzer.Result) {
    for _, r := range rs {
        m.counter.WithLabelValues(r.RuleName, r.Fingerprint).Inc()
    }
}
```

`RuleName` and `Fingerprint` are the two fields designed to be labels.
`Query` is for humans and `Message` for context; neither should be a metric
dimension.
