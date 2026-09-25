---
id: n-plus-one
title: N+1 Detection
description: How the runtime middleware spots the same query repeating in a loop, how the window works, and how to scope detection to a request.
---

# N+1 Detection

An N+1 is the loop that loads a list, then issues one more query per row:

```go
rows, _ := db.Query("SELECT id FROM orders WHERE customer_id = $1", cid)
for rows.Next() {
    var id int
    rows.Scan(&id)
    db.QueryRow("SELECT status FROM shipments WHERE order_id = $1", id) // ×N
}
```

Nothing about any single statement is wrong, which is why static rules
cannot catch it. The runtime middleware can, because it sees the sequence.

## Enabling it

```go
sqlguard.Register("sqlguard-pg", "pgx",
    middleware.WithN1Detection(5, 2*time.Second), // 5 hits in 2s → report
)
```

`WithN1Detection(threshold, window)` is off by default. _Added in 0.3._ It can
also be switched on from [`.sqlguard.yml`](configuration), which is the only
way to enable it without a code change:

```yaml
rules:
  settings:
    n-plus-one:
      threshold: 5      # both keys are required; one alone does nothing
      window: 2s
```

`WithN1Detection` in code wins over those values, but `disable: [n-plus-one]`
in the file switches detection off regardless. When enabled, every
executed statement is reduced to its [fingerprint](redaction) — literals
replaced, whitespace collapsed, `IN (?, ?, ?)` folded to `IN (?)` — and
counted. When the same fingerprint reaches `threshold` executions inside
`window`, one finding is emitted:

```text
[SQLGUARD WARNING] n-plus-one
  Query: SELECT status FROM shipments WHERE order_id = ?
  Issue: Possible N+1 query detected: same pattern executed 5 times in 2s
  Fix:   Consider using a JOIN or IN clause to batch these queries.
```

Because the key is the fingerprint, `WHERE order_id = 1`, `= 2`, `= 3` and
`= $1` all count as the same pattern — which is exactly what an N+1 looks
like from the driver.

## How the window behaves

- The window starts at the first sighting of a fingerprint. The finding
  fires the moment the count reaches `threshold` within it.
- Each fingerprint is reported **once per window**. When the window
  expires, the count resets and the pattern can be reported again.
- The tracker is bounded at 10,000 distinct fingerprints. Past that, expired
  entries are evicted first; if every entry is still live, a new pattern is
  dropped rather than the map grown — a rare false negative under
  pathological query-shape cardinality, never a memory leak. Patterns already
  being tracked are always honored.
- N+1 findings are not subject to [finding de-duplication](noise-control);
  the once-per-window rule above is their own emission policy.

## Scoping to a request

On the plain `database/sql` path you get back a `*sql.DB` and nothing else,
so detection is process-wide and windowed: a hot endpoint that legitimately
runs the same lookup for many different users inside two seconds will look
like an N+1. Tune `threshold` and `window` to your traffic, or use one of
the [integrations](integrations) — every one of them holds the guard and
exposes `ResetN1()`:

```go
tracer := pgxguard.NewTracer(middleware.WithN1Detection(10, time.Second))

func handler(w http.ResponseWriter, r *http.Request) {
    defer tracer.ResetN1() // detection scoped to this request
    // ...
}
```

Calling `ResetN1()` at a request boundary clears the counts, so a pattern
only trips the rule when it repeats **within one unit of work**. It is a
no-op when N+1 detection is not enabled.

If you build your own integration on [`middleware.Guard`](middleware#guard-for-integration-authors),
`Guard.ResetN1()` is the same call.

## What it will not catch

- Loops that use different SQL text per iteration in a way that survives
  fingerprinting (for example a table name interpolated per row). That is a
  separate anti-pattern; the static rules will usually flag the
  interpolation site.
- Queries issued from a library that bypasses `database/sql` entirely, unless
  it has an integration — native pgx/pgxpool needs [pgxguard](pgx).
