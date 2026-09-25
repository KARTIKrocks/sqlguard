---
id: analyzer
title: Analyzer API
description: Use the analyzer directly, pick a rule subset, write your own rules against the normalized Statement, register them by name, and write a custom reporter.
---

# Analyzer API

Everything above the driver is ordinary Go you can call yourself. The
`analyzer` package has no dependencies outside the standard library.

```go
import "github.com/KARTIKrocks/sqlguard/analyzer"
```

## Analyze a query

```go
a := analyzer.Default() // every built-in rule at its default severity

for _, r := range a.Analyze("DELETE FROM users") {
    fmt.Printf("[%s] %s: %s\n", r.Severity, r.RuleName, r.Message)
}
// [CRITICAL] delete-without-where: DELETE without WHERE clause detected. This will delete all rows.
```

`Analyze(query string) []analyzer.Result` parses once, runs every rule,
applies in-SQL [suppressions](suppressions) and severity overrides, and
sets `Query` (redacted) and `Fingerprint` on each result. It never errors
or panics; SQL the parser cannot understand still gets a best-effort pass
through the fallback parser.

### `Result`

| Field | Meaning |
| --- | --- |
| `RuleName string` | Stable rule identifier, e.g. `select-star`. |
| `Severity analyzer.Severity` | `SeverityInfo`, `SeverityWarning`, `SeverityCritical`. `String()` gives `INFO` / `WARNING` / `CRITICAL`. |
| `Query string` | The offending SQL, [redacted](redaction) unless the analyzer was built `WithRawQuery()`. |
| `Fingerprint string` | Always set. PII-free, low-cardinality query identity. |
| `Message string` | What was detected. |
| `Suggestion string` | How to fix it. May be empty. |
| `File string`, `Line int` | Set only by the static scanner. |

## Constructors

| Constructor | Rules | Configurable by name? |
| --- | --- | --- |
| `analyzer.Default()` | every registered rule | yes |
| `analyzer.DefaultWithProfile(p analyzer.Profile)` | registered rules filtered/tuned by the profile | yes |
| `analyzer.New(rules ...analyzer.Rule)` | exactly the functions you pass | no — anonymous rules have no name to configure |

And two copying modifiers:

```go
a = a.WithParser(pgparser.New()) // swap the parser; nil resets to the fallback
a = a.WithRawQuery()             // keep literals in Result.Query — local debugging only
```

### `Profile`

`Profile` is the resolved, parser-independent view of configuration. The
`config` package builds one from `.sqlguard.yml`; you can build one by
hand:

```go
p := analyzer.Profile{
    Disabled: map[string]bool{"orderby-without-limit": true},
    Severity: map[string]analyzer.Severity{"select-star": analyzer.SeverityInfo},
    Settings: map[string]analyzer.Settings{
        "large-offset": {"threshold": 5000},
    },
}
a := analyzer.DefaultWithProfile(p)
```

`Only` (a whitelist) and `RawQuery` are the other fields. Everything in
the profile is resolved once at construction — the per-query path does no
configuration work, which is what keeps it cheap.

## Writing a rule

A rule is a function over the normalized statement:

```go
type Rule func(s *analyzer.Statement) (analyzer.Result, bool)
```

It returns `(result, true)` to report, `(Result{}, false)` to stay quiet.
Rules read `Statement` fields; they never re-parse or pattern-match the raw
SQL — that is the parser's job, and it is what keeps rules correct across
the fallback and the real grammars.

```go
func checkSelectForUpdateWithoutLimit(s *analyzer.Statement) (analyzer.Result, bool) {
    if s.Kind == analyzer.StmtSelect && s.HasOrderBy && !s.HasLimit && !s.HasWhere {
        return analyzer.Result{
            RuleName:   "unbounded-sorted-select",
            Message:    "Sorted SELECT with no WHERE and no LIMIT sorts the whole table.",
            Suggestion: "Add a WHERE filter or a LIMIT.",
        }, true
    }
    return analyzer.Result{}, false
}
```

Leave `Severity`, `Query` and `Fingerprint` unset when the rule is
registered (below): the registry's default severity, profile overrides
and the redaction policy are applied centrally. Set `Severity` yourself
only for anonymous rules passed to `analyzer.New`.

Treat a `false` boolean as "not detected", not "proven absent" — the
fallback parser leaves a field `false` when it genuinely cannot tell, and
a rule that assumes otherwise produces false positives. `Statement.Exact`
tells you whether a real grammar produced the structural fields.

### Registering it by name

```go
func init() {
    analyzer.Register(analyzer.RuleSpec{
        Name:            "unbounded-sorted-select",
        DefaultSeverity: analyzer.SeverityWarning,
        Factory: func(s analyzer.Settings) analyzer.Rule {
            return checkSelectForUpdateWithoutLimit
        },
    })
}
```

Once registered, the rule is part of `analyzer.Default()` and is
addressable by name everywhere: `rules.disable` / `rules.severity` /
`rules.settings` in [config](configuration), `sqlguard:ignore:<name>` in
[suppressions](suppressions), and `analyzer.RuleNames()`. Registering a
name that already exists **replaces** the built-in, so you can override
one.

The `Factory` receives the rule's `Settings` map (from
`rules.settings.<name>` in config). Nil-safe accessors — `Int`, `Bool`,
`String`, `Duration`, each with a default — let a rule take tunables
without touching the config schema:

```go
Factory: func(s analyzer.Settings) analyzer.Rule {
    max := s.Int("max-rows", 10000)
    return func(st *analyzer.Statement) (analyzer.Result, bool) { /* use max */ }
},
```

### Using a subset

```go
a := analyzer.New(
    analyzer.CheckDeleteWithoutWhere,
    analyzer.CheckUpdateWithoutWhere,
)
```

The built-in rule functions (`CheckSelectStar`, `CheckLeadingWildcard`,
`CheckDeleteWithoutWhere`, `CheckUpdateWithoutWhere`,
`CheckInsertWithoutColumns`, `CheckSelectWithoutLimit`,
`CheckOrderByWithoutLimit`, `CheckNonSargablePredicate`,
`CheckAddNotNullWithoutDefault`, `CheckImplicitJoin`,
`CheckCartesianJoin`, `CheckInListTooLarge`, `CheckLargeOffset`,
`CheckSelectDistinct`) are exported. Rules passed to `New` are anonymous:
profile overrides do not apply, and the tunable ones run at their
defaults. Prefer `DefaultWithProfile` with `Only` when you want a named,
configurable subset.

## Helpers

| Function | Use |
| --- | --- |
| `analyzer.Redact(sql string) string` | Literals → `?`, comments stripped, structure kept. |
| `analyzer.Fingerprint(sql string) string` | `Redact` + whitespace collapse + list fold. |
| `analyzer.IsMultiStatement(sql string) bool` | Comment- and string-aware `;` check. |
| `analyzer.ParseIgnoreComment(text string) (all bool, rules map[string]bool, found bool)` | Parse a Go comment for a suppression directive. |
| `analyzer.RuleNames() []string` | Every registered rule name, sorted. |
| `analyzer.NewFallbackParser() *FallbackParser` | The zero-dependency parser, for delegation. |
| `(*Analyzer).PrepareQuery(raw string) (display, fingerprint string)` | Apply this analyzer's redaction policy to a query outside the rule path. |

## Reporters

```go
import "github.com/KARTIKrocks/sqlguard/reporter"

type Reporter interface {
    Report(results []analyzer.Result)
}
```

| Reporter | Output |
| --- | --- |
| `reporter.NewConsoleReporter()` / `NewConsoleReporterTo(w)` | Colored, human-readable blocks; stderr by default. |
| `reporter.NewJSONReporter()` / `NewJSONReporterTo(w)` | A JSON array; stderr by default. |

`Report` must be safe for concurrent calls — the middleware invokes it
from whichever goroutine ran the query. The result slice handed to you may
be shared with the analysis cache; treat it as read-only.

A reporter that forwards to `slog`:

```go
type slogReporter struct{ l *slog.Logger }

func (s *slogReporter) Report(rs []analyzer.Result) {
    for _, r := range rs {
        s.l.Warn("sqlguard finding",
            "rule", r.RuleName,
            "severity", r.Severity.String(),
            "fingerprint", r.Fingerprint,
            "query", r.Query,
            "message", r.Message,
        )
    }
}

sqlguard.Register("sqlguard-pg", "pgx", middleware.WithReporter(&slogReporter{l: slog.Default()}))
```
