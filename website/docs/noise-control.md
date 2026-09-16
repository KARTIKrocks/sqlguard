---
id: noise-control
title: Noise Control
description: Finding de-duplication and the per-query analysis cache — what keeps sqlguard quiet and cheap on a hot query path.
---

# Noise Control

A query that runs ten thousand times a minute would, naively, produce ten
thousand identical warnings and ten thousand parses. Two mechanisms make the
runtime middleware quiet and cheap: **finding de-duplication** decides
whether a finding is _reported_, and the **analysis cache** decides whether a
query is _analyzed_. They are independent and both are on by default.

## Finding de-duplication

A finding's identity is the pair **(rule, fingerprint)** — the same rule
firing on the same canonical query shape. By default the middleware reports
each identity **at most once per minute**:

```go
sqlguard.Register("sqlguard-pg", "pgx",
    middleware.WithFindingDedup(5*time.Minute), // quieter
)
sqlguard.Register("sqlguard-pg", "pgx",
    middleware.WithFindingDedup(0), // report every occurrence
)
```

Or in [`.sqlguard.yml`](configuration):

```yaml
dedup:
  window: 1m # "0" disables
```

Because the key is the [fingerprint](redaction) rather than the raw query,
`SELECT * FROM users WHERE id = 1` and `... WHERE id = 2` are the same
finding — one `select-star` warning per window, not one per user.

What de-duplication does **not** touch:

- `n-plus-one` — the tracker has its own once-per-window policy; see
  [N+1 detection](n-plus-one).
- `slow-query` — reported on every slow execution on purpose. A query that
  is slow three times in a row is three data points, not one.
- The static scanner and EXPLAIN analyzer — they run once per invocation,
  so there is nothing to de-duplicate.

The de-duplicator tracks up to 10,000 identities. At capacity it evicts
expired entries; if every entry is still inside its window, a _new_
identity is dropped rather than the map grown without bound. Identities
already being tracked are always updated.

## Analysis cache

Static analysis — parse, build the normalized `Statement`, run every rule —
is the expensive part of `Guard.Check`. The middleware memoizes it per
**exact query string** in a bounded LRU:

```go
sqlguard.Register("sqlguard-pg", "pgx",
    middleware.WithAnalysisCacheSize(4096), // default 1024
)
sqlguard.Register("sqlguard-pg", "pgx",
    middleware.WithAnalysisCacheSize(0), // analyze every execution
)
```

A cache hit is a mutex-guarded map lookup with zero allocations — about
20 ns on a laptop, versus roughly 22 µs for a full analysis with the
default rule set. Parameterized queries (the common case) and repeated
identical strings hit; a query whose literals vary per call misses, and has
to, because its findings can differ.

### Why the exact string, not the fingerprint

The fingerprint folds literals away, but three rules read facts that live
in the literals: `large-offset` (the `OFFSET` value), `in-list-too-large`
(the element count) and `leading-wildcard` (`min-length` of the search
term). Two queries can share a fingerprint and deserve different verdicts —
`OFFSET 10` and `OFFSET 100000` — so keying on the fingerprint would cache
a wrong answer. Identical strings always analyze identically, which makes
the exact string the only fully correct key.

### Interaction with the parser

The cache sits in front of whichever [parser](parsers) is configured. With
a real grammar the uncached cost is higher, which makes the cache matter
more, not less.

## Putting it together

For a request that runs the same three parameterized statements:

1. First execution of each: full analysis, findings reported, cache and
   de-duplicator populated.
2. Every execution after that, within the window: cache hit; findings are
   found again but suppressed by the de-duplicator; N+1 still counted;
   latency still measured.
3. After the window: the next execution re-reports the finding, once.

The cost of the steady state is the cost of step 2 — a map lookup and a
timestamp comparison.
