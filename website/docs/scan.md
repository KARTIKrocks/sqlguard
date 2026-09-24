---
id: scan
title: Static Scanner
description: sqlguard scan — find SQL issues in Go source without running the application, wire it into CI, and understand what it can and cannot resolve.
---

# Static Scanner

`sqlguard scan` walks Go source, finds the calls that send SQL to a
database, recovers the query text, and runs the same static
[rules](rules) the runtime middleware runs. No database, no running
application, no test fixtures — it fits in a pre-commit hook or a CI step.

## Usage

```bash
go install github.com/KARTIKrocks/sqlguard/cmd/sqlguard@latest

sqlguard scan .                       # current module
sqlguard scan ./internal/repository   # one package tree
sqlguard scan --format json ./...     # machine-readable
```

The scan is always recursive, so a path may be written plainly (`./internal`)
or with the Go package-pattern suffix (`./internal/...`); both select the same
files. With no path at all it scans the current directory.

_Changed in 0.3._ In 0.2 the pattern spelling was rejected outright —
`sqlguard scan ./...` failed with `lstat ./...: no such file or directory` —
so the form used throughout these docs had to be written as `sqlguard scan .`.

| Flag | Default | Effect |
| --- | --- | --- |
| `--format console\|json` | `console` | Output shape. JSON is an array of `{rule, severity, query, fingerprint, message, suggestion, file, line}`. |
| `--config <path>` | auto-discover | Load a specific [`.sqlguard.yml`](configuration). |
| `--no-config` | — | Ignore any config file; run every rule at its default. |

Exit code is **1** when any issue is found and **0** when clean.

Output is split by audience. The **console** format writes findings and the
summary line to **stderr**, so `2>` captures the human-readable report without
disturbing whatever your CI step prints to stdout. The **JSON** format writes
to **stdout**, so `--format json > findings.json` and pipes into `jq` both
work, and it always emits an array — `[]` on a clean run — so a consumer never
has to parse an empty file.

_Changed in 0.3._ In 0.2 JSON also went to stderr, which made
`--format json > findings.json` produce an empty file, and a clean run printed
nothing at all rather than `[]`.

```text
[SQLGUARD CRITICAL] delete-without-where
  File: internal/repo/sessions.go:42
  Query: DELETE FROM sessions
  Issue: DELETE without WHERE clause detected. This will delete all rows.
  Fix:   Add a WHERE clause to limit the scope of the delete.

[SQLGUARD WARNING] select-star
  File: internal/repo/users.go:17
  Query: SELECT * FROM users WHERE id = $1
  Issue: SELECT * detected. Selecting all columns can hurt performance.
  Fix:   Select only the columns you need.

2 issue(s) found (17 file(s) scanned)
```

`File` and `Line` point at the call, not at the string constant — that is
where the fix goes.

## What it looks for

Any call whose method name is one of `Query`, `QueryContext`, `QueryRow`,
`QueryRowContext`, `Exec`, `ExecContext`, `Prepare` or `PrepareContext`, on
any receiver — `*sql.DB`, `*sql.Tx`, `*sql.Conn`, an `sqlx.DB`, a repository
interface of your own. The first argument is taken as the SQL — the second
for the `…Context` variants, whose first argument is the `ctx`.

`_test.go` files, hidden directories, `vendor/` and `node_modules/` are
skipped, plus anything matching `scan.exclude-paths` in your config.

## What it can resolve

The scanner type-checks the target with `golang.org/x/tools/go/packages`,
so the query does not have to be an inline literal:

| Argument shape | Resolved? |
| --- | --- |
| `db.Query("SELECT …")` | yes |
| `db.Query(selectUsers)` — a `const` in the same package | yes |
| `db.Query(queries.SelectUsers)` — a `const` in another package | yes |
| `db.Query("SELECT id " + "FROM users")` — constant concatenation | yes |
| `db.Query(fmt.Sprintf("SELECT * FROM %s WHERE id = %d", table, id))` | yes — the format string is analyzed with verbs neutralized |
| `db.Query(buildQuery(filters))` — a runtime value | no |
| `db.Query(q)` where `q` is a `var` | no |

For `fmt.Sprintf`, numeric verbs become `0` and everything else becomes a
placeholder identifier, so the SQL keeps enough structure for the rules:
`SELECT * FROM %s` still trips `select-star`; `LIMIT %d` still counts as a
`LIMIT`.

If the target is not a loadable module (no `go.mod`, or a build failure),
the scanner falls back to a plain `go/parser` walk that still resolves
inline literals, so a broken tree is reported rather than silently skipped.

## Suppressing a finding

Add a comment on the call line or the line directly above:

```go
// sqlguard:ignore:delete-without-where
db.Exec("DELETE FROM sessions")
```

Or inside the SQL itself, which also works at runtime. Details in
[Suppressions](suppressions).

## In CI

```yaml
# .github/workflows/ci.yml
- uses: actions/setup-go@v7
  with:
    go-version: "1.27"
- run: go install github.com/KARTIKrocks/sqlguard/cmd/sqlguard@latest
- run: sqlguard scan ./...
```

To keep the findings as a build artifact, redirect the JSON and let the exit
code still fail the step:

```yaml
- run: sqlguard scan --format json ./... > sqlguard.json
- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: sqlguard-findings
    path: sqlguard.json
```

The step fails on any finding. To gate only on the serious ones, lower the
noisy rules in `.sqlguard.yml`:

```yaml
rules:
  severity:
    select-distinct: "off"
    orderby-without-limit: "off"
```

Severity does not affect the exit code — any reported finding is a
non-zero exit — so use `"off"` (or `rules.disable`) for rules you do not
want to gate on, rather than `info`.

## Limits

- Runtime-only rules (`n-plus-one`, `slow-query`) cannot fire statically.
- Only values the type checker can fold are seen. A query assembled at
  runtime is invisible here — that is what the [middleware](middleware) is
  for.
- The default [fallback parser](parsers) is best-effort. The scanner does
  not currently accept a parser flag; it uses the analyzer your config
  produces.
