---
id: configuration
title: Configuration
description: The .sqlguard.yml file — every key, how it is discovered, how the CLI and the middleware load it, and the config package API.
---

# Configuration

One file, `.sqlguard.yml`, configures every surface: which rules run, at
what severity, with which tunables, plus the runtime thresholds and the
scanner's exclusions. It is optional — with no file, every rule runs at its
default.

## Discovery

sqlguard looks for `.sqlguard.yml` (or `.sqlguard.yaml`) starting in the
scanned or working directory and walking **up** until it finds one or
reaches the git root (a directory containing `.git`). Put it at the
repository root and every sub-package inherits it.

The CLI accepts `--config <path>` to load a specific file and `--no-config`
to ignore any file and use built-in defaults. Both are persistent flags, so
they work with `scan` and `explain` alike.

## Full reference

```yaml
version: 1

# Turn soft problems (unknown keys, unknown rule names, bad severities)
# into hard errors. Leave false so a config written for a newer sqlguard
# still loads on an older binary.
strict: false

rules:
  # Turn rules off entirely.
  disable:
    - orderby-without-limit

  # Whitelist mode: when non-empty, ONLY these rules run (disable is ignored).
  # only:
  #   - delete-without-where
  #   - update-without-where

  # Override the reported severity per rule: info | warning | critical | off.
  # "off" is equivalent to disabling the rule.
  severity:
    select-star: info
    select-without-limit: "off"

  # Per-rule tunables. Keys are rule-specific; see the Rules reference.
  settings:
    leading-wildcard:
      min-length: 3       # ignore LIKE '%x%' with a searchable term shorter than this
    in-list-too-large:
      max-length: 100     # flag IN (...) with more elements than this
    large-offset:
      threshold: 1000     # flag a literal OFFSET above this
    slow-query:
      threshold: 200ms    # runtime: flag a query at or above this latency
    n-plus-one:
      threshold: 10       # runtime: this many of the same fingerprint...
      window: 1m          # ...within this window

# Redact literal values out of Result.Query. ON by default. Set false ONLY
# for local debugging where the query text is trusted.
redact: true

# Runtime middleware: report each (rule, fingerprint) at most once per
# window. "0" disables and reports every occurrence.
dedup:
  window: 1m

# Static scanner only: skip files whose path matches any of these regexes.
scan:
  exclude-paths:
    - "(^|/)legacy/"
    - "_gen\\.go$"
```

| Key | Applies to | Notes |
| --- | --- | --- |
| `version` | all | Reserved for forward compatibility; always `1` today. |
| `strict` | all | Make unknown keys, unknown rule names and bad severities fatal instead of warnings. |
| `rules.disable` | every rule | Rule names to turn off. |
| `rules.only` | the scanner and the statement rules at runtime | Whitelist over the rules evaluated against a statement. `disable` still applies to the ones listed, so listing and disabling the same rule disables it. It does **not** reach `slow-query`, `n-plus-one` or the plan rules — see below. |
| `rules.severity` | every rule | `info`, `warning`, `critical`, or `off`. |
| `rules.settings` | rules with tunables | `leading-wildcard.min-length`, `in-list-too-large.max-length`, `large-offset.threshold`, `slow-query.threshold`, `n-plus-one.threshold` / `.window`. See [Rules](rules). |
| `redact` | all | `false` keeps raw literals in `Result.Query`. See [Redaction](redaction). |
| `dedup.window` | middleware, integrations | Go duration or `"0"`. Equivalent to `WithFindingDedup`. |
| `scan.exclude-paths` | scanner | Regexes matched against the scanned file path. |

Quote `"off"` — unquoted `off` is a YAML boolean.

_Changed in 0.3._ "Every rule" now means every rule. In 0.2 only the 14
statement rules were addressable: naming `slow-query`, `n-plus-one` or a plan
rule (`seq-scan`, `high-cost`, `full-table-scan`, `no-index-used`, `filesort`)
warned with `unknown rule`, and failed outright under `strict: true`, even
though the [rules reference](rules) listed them. The slow-query threshold also
moved from a top-level `slow-query.threshold` key to
`rules.settings.slow-query.threshold`, so every tunable lives in one place.

## Lenient by default

Unknown top-level keys and unknown rule names are **warnings**, printed to
stderr by the CLI as `sqlguard: config warning: …`, so a config that names
a rule added in a newer release still loads on an older binary. Set
`strict: true` when you want CI to fail on a typo.

_Added in 0.3._ N+1 detection can be turned on from the file. Setting both
`rules.settings.n-plus-one.threshold` and `.window` enables it; previously it
was reachable only from Go with `WithN1Detection`, which remains the way to
set it in code. An explicit Go option wins over the file for the N+1 and slow-query
_thresholds_. Turning either rule off goes the other way — see
[Precedence](#precedence).

## Loading it from Go

The `config` package is the only place YAML is parsed. `analyzer` and
`middleware` never import it, so users who configure in code pay for no
YAML dependency.

```go
import "github.com/KARTIKrocks/sqlguard/config"
```

| Function | Returns |
| --- | --- |
| `config.Middleware(path, startDir string) ([]middleware.Option, error)` | Ready-to-use options. `path == ""` discovers from `startDir`. A missing file is not an error — it yields the defaults. |
| `config.Load(path string) (*Config, error)` | Parse one file. |
| `config.Discover(startDir string) (*Config, string, error)` | Walk up from `startDir`; returns the config and the path it came from (`""` if none). |
| `config.Default() *Config` | The empty config: every rule at its defaults. |

And on a `*Config`:

| Method | Use |
| --- | --- |
| `MiddlewareOptions() ([]middleware.Option, error)` | `WithAnalyzer` from the profile — which carries the rule settings, including the slow-query and N+1 tunables — plus `WithFindingDedup` when set. Append your own options after it. |
| `Analyzer() (*analyzer.Analyzer, error)` | `analyzer.DefaultWithProfile` built from this file. |
| `Profile() (analyzer.Profile, error)` | The resolved, parser-independent profile. |
| `DedupWindow()` | `(time.Duration, ok bool, error)` — `ok` is false when the key is unset. _Changed in 0.3._ `SlowQueryThreshold()` is gone; take the profile first and read the setting off it (see below). |
| `ExcludeMatcher() (func(path string) bool, error)` | The compiled `scan.exclude-paths` predicate. |
| `Warnings() []string` | Non-fatal problems found while loading. Surface them. |

The slow-query threshold now lives in the profile with every other tunable:

```go
p, err := cfg.Profile()
if err != nil {
    return err
}
d := p.Settings["slow-query"].Duration("threshold", 200*time.Millisecond)
```

The common case is one line:

```go
opts, err := config.Middleware("", ".")
if err != nil {
    log.Fatal(err)
}
opts = append(opts, middleware.WithParser(pgparser.New()))
sqlguard.Register("sqlguard-pg", "pgx", opts...)
```

## Precedence

_Changed in 0.3._ For the **thresholds**, an explicit Go option wins over the
file wherever it appears in the list — `WithSlowQueryThreshold` and
`WithN1Detection` record that they were called, so a config value no longer
has to be ordered around. In 0.2 this depended on option order, because the
file's threshold arrived as an option of its own.

```go
opts, _ := cfg.MiddlewareOptions()
opts = append(opts, middleware.WithSlowQueryThreshold(time.Second))
// 1s, whatever rules.settings.slow-query.threshold says
```

**Turning a rule off is the other way round: the file wins.** `disable:
[slow-query]` silences the finding even with `WithSlowQueryThreshold` set, and
`disable: [n-plus-one]` stops the tracker being built at all despite
`WithN1Detection`. That is deliberate — `disable` is an instruction, not a
tuning value, and an operator editing `.sqlguard.yml` should be able to
silence a noisy rule without a redeploy.

`only:` narrows; it does not override. A rule has to survive both checks, so
`only: [select-star]` together with `disable: [select-star]` leaves nothing.

## What `only:` reaches

`only:` selects which rules run **against a statement** — the 14 in the
scanner and at runtime. It does not reach the seven findings that are not
derived from statement text: `slow-query` and `n-plus-one`, which the
middleware computes from latency and repetition, and the five plan rules
[`sqlguard explain`](explain) reads from the database's own plan.

That is because a whitelist is nearly always written to focus a scan, and it
names statement rules. If it reached the rest, `only: [select-star]` in a
repository's config would also switch off latency and N+1 reporting in the
running application, and make `sqlguard explain` report nothing — none of
which it mentions, and none of which would produce a warning.

To switch one of those off, name it: `disable:` and `severity: off` reach
every surface.

```yaml
rules:
  only: [select-star]        # scanner + runtime statement rules
  disable: [slow-query]      # and this reaches the middleware too
```

Inline [suppressions](suppressions) win over both: they silence a finding at
one site regardless of config.
