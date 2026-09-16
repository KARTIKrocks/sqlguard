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

# Redact literal values out of Result.Query. ON by default. Set false ONLY
# for local debugging where the query text is trusted.
redact: true

# Runtime middleware: slow-query threshold. Go duration string.
slow-query:
  threshold: 200ms

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
| `rules.disable` | static + runtime rules | Rule names to turn off. |
| `rules.only` | static + runtime rules | Whitelist. When non-empty, only these run and `disable` is ignored. |
| `rules.severity` | static + runtime rules | `info`, `warning`, `critical`, or `off`. |
| `rules.settings` | rules with tunables | `leading-wildcard.min-length`, `in-list-too-large.max-length`, `large-offset.threshold`. See [Rules](rules). |
| `redact` | all | `false` keeps raw literals in `Result.Query`. See [Redaction](redaction). |
| `slow-query.threshold` | middleware, integrations | Go duration (`200ms`, `1s`). Equivalent to `WithSlowQueryThreshold`. |
| `dedup.window` | middleware, integrations | Go duration or `"0"`. Equivalent to `WithFindingDedup`. |
| `scan.exclude-paths` | scanner | Regexes matched against the scanned file path. |

Quote `"off"` — unquoted `off` is a YAML boolean.

## Lenient by default

Unknown top-level keys and unknown rule names are **warnings**, printed to
stderr by the CLI as `sqlguard: config warning: …`, so a config that names
a rule added in a newer release still loads on an older binary. Set
`strict: true` when you want CI to fail on a typo.

One thing people look for and do not find: N+1 detection has no config key.
Its `threshold` and `window` are workload-specific, so they are set in code
with `WithN1Detection` (see [N+1 detection](n-plus-one)).

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
| `MiddlewareOptions() ([]middleware.Option, error)` | `WithAnalyzer` from the profile, plus `WithSlowQueryThreshold` / `WithFindingDedup` when set. Append your own options after it. |
| `Analyzer() (*analyzer.Analyzer, error)` | `analyzer.DefaultWithProfile` built from this file. |
| `Profile() (analyzer.Profile, error)` | The resolved, parser-independent profile. |
| `SlowQueryThreshold()`, `DedupWindow()` | `(time.Duration, ok bool, error)` — `ok` is false when the key is unset. |
| `ExcludeMatcher() (func(path string) bool, error)` | The compiled `scan.exclude-paths` predicate. |
| `Warnings() []string` | Non-fatal problems found while loading. Surface them. |

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

Options given in code after `MiddlewareOptions()` win, because
`middleware.Option`s apply in order. So `append(opts,
middleware.WithSlowQueryThreshold(time.Second))` overrides the file's
`slow-query.threshold`. Inline [suppressions](suppressions) always win over
both: they silence a finding at one site regardless of config.
