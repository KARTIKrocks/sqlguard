---
id: parsers
title: SQL Parsers
description: The zero-dependency fallback parser, the opt-in PostgreSQL and MySQL grammars, which facts each derives structurally, and how parse failures degrade.
---

# SQL Parsers

Rules never read raw SQL. They read an `analyzer.Statement` — a small,
dialect-agnostic struct of facts (`Kind`, `HasWhere`, `HasLimit`,
`SelectStar`, `OffsetValue`, …) that a `Parser` produces. That seam is what
lets the same rules run on a regex-free fallback and on a real grammar.

```go
type Parser interface {
    Parse(sql string) (*Statement, error)
}
```

## The default: `FallbackParser`

Zero dependencies, ships in the core module, never returns an error. It
strips comments and string-literal contents before looking at the
statement, so keywords inside comments or strings and identifiers like
`update_at` do not cause false positives. CTEs, subqueries and driver
placeholders (`$1`, `?`, `:name`) are handled well enough for every
built-in rule.

It is best-effort by design. A fact it cannot determine is left `false`
(or zero), and rules treat that as "not detected", never "proven absent"
— so the failure mode is a missed finding, not a spurious one. Every
`Statement` it produces has `Exact == false`.

## Opt-in real grammars

For structural certainty, add a dialect parser. Each lives in its own Go
module so the grammar dependency never enters your build unless you import
it:

```bash
go get github.com/KARTIKrocks/sqlguard/parsers/pgparser     # PostgreSQL — auxten/postgresql-parser, pure Go
go get github.com/KARTIKrocks/sqlguard/parsers/mysqlparser  # MySQL — xwb1989/sqlparser (Vitess-derived), pure Go
```

```go
import "github.com/KARTIKrocks/sqlguard/parsers/pgparser"

// Runtime middleware or any integration:
sqlguard.Register("sqlguard-pg", "pgx", middleware.WithParser(pgparser.New()))

// Standalone analyzer:
a := analyzer.Default().WithParser(pgparser.New())
```

`middleware.WithParser` applies to whichever analyzer is in use, including
one loaded from config, so `append(opts, middleware.WithParser(...))` is
enough.

Neither parser uses cgo.

## What a real parser changes

| Fact | Fallback | Real parser |
| --- | --- | --- |
| `Kind` (SELECT / INSERT / UPDATE / DELETE / other) | lexical | AST |
| `HasWhere`, `HasLimit`, `HasOrderBy`, `HasFrom` | lexical | AST — correct through CTEs, subqueries, dialect syntax |
| `SelectStar`, `SelectDistinct` | lexical | AST — `COUNT(*)` and `COUNT(DISTINCT x)` never confuse it |
| `InsertColumnsListed` | lexical | AST |
| `OffsetValue` | lexical | AST limit clause |
| `MaxInListLen` (`in-list-too-large`) | lexical | **still lexical** — the AST discards the literal list |
| `ImplicitCommaJoin`, `CartesianJoin` | lexical | **still lexical** — deliberately text-level |
| `LeadingWildcardLike`, `LeadingWildcardTermLen`, `NonSargablePredicate`, `AddNotNullNoDefault` | lexical | **still lexical** — they read literal values or DDL text the AST does not carry |

The first group is the false-positive-prone set; those become exact
(`Statement.Exact == true`) for the statements each parser models:
`SELECT` (including set operations), `INSERT`, `UPDATE` and `DELETE`.
_Changed in 0.6._ A statement the grammar accepts but the parser does not
model — DDL, `EXPLAIN`, `CREATE VIEW … AS SELECT` — keeps the fallback's
facts and `Exact == false`; before, every structural field on it was
cleared and the statement was still marked exact. The rest stay best-effort heuristics
regardless of the parser, and each field's doc comment says so. This is a
documented carve-out, not a gap waiting to be closed.

_Added in 0.5._ **A dialect parser is meant to remove findings, never add
them.** Opting in can drop a finding the heuristics guessed wrong; it
should not report one the fallback would not have. Each parser module
pins this direction against a corpus
(`TestParser_NeverAddsFindingTheFallbackDoesNot`), so a rule that reads a
structural field the grammar fills differently from the fallback fails
there rather than reaching you. That is a tested invariant over a corpus,
not a proof: a dialect form neither the corpus nor the fallback's keyword
list knows is how it would break again.

In 0.4 and earlier it did break, in one rule, on every statement the
grammar recognised as inserting rows and the fallback did not:

- `pgparser` reported `insert-without-columns` on
  `INSERT INTO t DEFAULT VALUES`, which names no columns and needs none,
  and on `UPSERT INTO t VALUES (…)`, plain or behind a CTE.
- `mysqlparser` reported it on `REPLACE INTO t VALUES (…)` and on the
  forms that omit MySQL's optional `INTO`, such as `INSERT t VALUES (…)`.

_Changed in 0.6._ **A dialect parser also should not drop a finding it
has no reason to drop.** Removing a finding is only an improvement when
the grammar derived something that disproves it. In 0.5 and earlier
both parsers dropped findings they had derived nothing about:

- `select-star` on `CREATE VIEW v AS SELECT * FROM t`,
  `CREATE TABLE c AS SELECT * FROM t` and `EXPLAIN SELECT * FROM t`,
  whose structural fields were cleared rather than kept from the fallback.
- `select-star` on `INSERT INTO t (a) SELECT * FROM u`, whose row source
  was never inspected.
- `select-star` and `select-without-limit` on
  `SELECT * FROM t UNION SELECT * FROM u`, whose operands were never read.
- Under `pgparser`, `select-without-limit` and `orderby-without-limit` on a
  bare `OFFSET` with no `LIMIT`, which was read as a limit. `LIMIT ALL`
  still counts as one: it states that no limit is wanted.

## Degradation on parse failure

A real grammar will reject SQL it does not know: dynamic fragments, a
dialect extension, a CTE form the MySQL grammar predates, a placeholder
style it does not model. When that happens the dialect parser **returns
the fallback parser's `Statement`** (`Exact == false`) — never an error.
And if a `Parser` you write yourself does return an error,
`analyzer.Analyze` catches it and re-parses with the fallback. Either way,
analysis never breaks the caller's `db.Query`.

## Writing a parser

Implement `Parse` and fill the `Statement` fields you can derive; leave the
rest zero. The contract on the runtime path:

- **Must not panic.** It runs inside every database call.
- **Should not error** for SQL it merely does not understand — degrade
  to a best-effort `Statement` instead. `analyzer.NewFallbackParser()` is
  exported so you can delegate to it.
- **Set `Exact`** only for the structural fields you actually computed
  from an AST.

Then pass it with `middleware.WithParser` or `analyzer.WithParser`.
