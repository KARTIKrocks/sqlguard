---
name: Bug report
about: Report incorrect behavior, a false positive/negative, or a crash
title: ""
labels: bug
assignees: ""
---

**Do not file security vulnerabilities here** — see [SECURITY.md](../../SECURITY.md).

## What happened

A clear description of the bug.

## Expected behavior

What you expected instead. For a false positive/negative, say which **rule**
(e.g. `select-star`) fired or failed to fire.

## Reproduction

The SQL or Go snippet, and how it was issued:

```sql
-- query (redacted is fine)
```

```go
// minimal repro
```

## Environment

- sqlguard version / commit:
- Affected module(s) (root, `integrations/<name>`, `parsers/<name>`):
- Parser in use (default fallback / pgparser / mysqlparser):
- Entry surface (runtime middleware / CLI `scan` / CLI `explain` / integration):
- Go version:
- Database + dialect (if relevant):

## Additional context

Logs (redaction-safe), config (`.sqlguard.yml`), or anything else useful.
