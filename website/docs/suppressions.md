---
id: suppressions
title: Inline Suppressions
description: Silence a finding at one call site with a comment in the SQL or in the Go source — no config file required.
---

# Inline Suppressions

Sometimes the rule is right in general and wrong here: the config table
really does have nine rows and `SELECT *` is fine; the nightly job really
does `DELETE FROM sessions`. A suppression silences a specific finding at a
specific site while leaving the rule on everywhere else. No config file is
needed.

## In the SQL

Put a `sqlguard:ignore` directive in a SQL comment anywhere in the statement:

```sql
SELECT * FROM feature_flags              -- sqlguard:ignore
DELETE FROM sessions                     /* sqlguard:ignore:delete-without-where */
SELECT * FROM t ORDER BY created_at      -- sqlguard:ignore:select-star, orderby-without-limit
```

- A bare `sqlguard:ignore` suppresses **every** rule for that statement.
- `sqlguard:ignore:rule-a,rule-b` suppresses only the named
  [rules](rules). Whitespace after the commas is fine.
- `--`, `/* */` and `#` comments all work; the directive is case-insensitive.

In-SQL directives are honored **everywhere the statement is analyzed**: the
runtime middleware, every integration, and the static scanner. Because the
text travels with the query, the suppression follows it through an ORM or a
query builder untouched.

### Why it must be in a comment

_Changed in 0.6._ The directive is honored only inside a SQL comment. Text
inside a string literal, a quoted identifier or a dollar-quoted body is
never read as one, so a value that happens to contain the words — a support
ticket body with `'-- sqlguard:ignore'` in it, or user input concatenated
into the query — suppresses nothing. Before 0.6 the directive only had to
follow a comment marker, and that marker could itself be inside the string.

Databases disagree about where some literals end, and where comments
start: whether `\'` escapes a quote, whether `$$` opens a string, whether
`#` is a comment (it is XOR in PostgreSQL), and whether `--` needs a space
after it (it does in MySQL). When the answer decides whether a
directive is in a comment, sqlguard does not suppress. The cost is at most a
finding a genuine but ambiguous directive failed to silence; the alternative
would let text inside a value switch rules off. `//` is not a SQL comment,
so `// sqlguard:ignore` inside SQL text is not honored either.

## In the Go source

For the [static scanner](scan), a Go comment on the call line or on the
line directly above it also works:

```go
// sqlguard:ignore
db.Exec("DELETE FROM sessions")

db.Query("SELECT * FROM feature_flags") // sqlguard:ignore:select-star

rows, err := tx.QueryContext(ctx,
    "SELECT * FROM t") // sqlguard:ignore  ← applies: this is the call's line
```

The scanner reads the file's comment map, so both `//` and `/* */`
comments qualify. The scope is exactly **the comment's own line and the
next line**; a directive two lines above a call does nothing.

Go-source suppressions are only visible to the scanner. The runtime
middleware sees SQL strings, not Go files, so a call site that must be
quiet at runtime too needs the in-SQL form.

## What suppression does not do

- It does not affect the `slow-query` or `n-plus-one` runtime findings. Those
  are turned off in [config](configuration) instead — `disable: [slow-query]`
  — which since 0.3 works for every rule name in the reference table.
  Those are about behaviour, not statement text; tune their thresholds via
  [options](middleware#options) or scope N+1 with `ResetN1()`.
- It does not affect the [EXPLAIN analyzer](explain), which reports on the
  plan.
- It is per statement. To turn a rule off for a whole project, use
  `rules.disable` in [`.sqlguard.yml`](configuration); to lower its
  severity, `rules.severity`.

## Programmatic use

`analyzer.ParseIgnoreComment(text string) (all bool, rules map[string]bool, found bool)`
is exported for tools that want to honor the Go-source form against their
own AST walk. It expects the comment text with the `//` or `/* */` already
stripped — which is how `go/ast` hands it to you.
