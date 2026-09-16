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
- Every single-quoted string literal becomes `?`, honoring `''` escapes.
- Every numeric literal becomes `?`.
- Keywords, structure, and identifiers — including `"double-quoted"` and
  `` `backtick` `` names — are preserved.

It never errors; unparseable input still comes out with its literals gone.

```text
in:  SELECT * FROM "users" WHERE email = 'a@b.c' AND age > 30 -- vip
out: SELECT * FROM "users" WHERE email = ? AND age > ?
```

`Result.Query` is set from `Redact` centrally in `Analyzer.Analyze`, and the
findings built outside the rule path — `slow-query` and `n-plus-one` — go
through `Analyzer.PrepareQuery`, which applies the same policy. There is one
normalizer in the codebase; nothing re-implements it.

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
it as `fingerprint`; `analyzer.Fingerprint(sql)` computes it directly.

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
