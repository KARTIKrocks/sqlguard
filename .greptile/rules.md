# Style and pattern rationale

Context for the scoped rules in `config.json`. This file is freeform prose read
alongside the diff; `config.json` is what actually gates comment scope and
severity.

## Fail closed, not silently blind — worked example

`fail-closed-on-unknown-shape` exists because of a real bug (fixed in
"Address CodeRabbit review: fail closed on unknown plan shape"): MySQL 9
returns a bare `EXPLAIN` in `TREE` format instead of the requested
`FORMAT=TRADITIONAL` shape under some conditions. `analyzeMySQL` looked up
plan columns by name into a map built from `rows.Columns()`; when the expected
columns weren't present, every lookup silently returned `""` and the analyzer
walked a row of empty strings, finding nothing to flag. The fix added an
explicit check — `table`/`type`/`key`/`possible_keys`/`extra` must all be
present — that turns a shape it doesn't recognize into a named error instead
of a quiet pass. The lesson generalizes past this one spot: anywhere this
codebase reads structured output whose shape depends on a server version, a
config file, or a parser it doesn't fully control, an unrecognized shape must
be a hard error, not a best-effort read that happens to come back empty. A
query linter that says "no issues" when it actually failed to look is worse
than one that visibly errors.

## Why EXPLAIN's read-only transaction has an exception

`explain/explain.go` uses `BeginTx(ReadOnly: !dml)`, not always
`ReadOnly: true`. This looks wrong at a glance — surely EXPLAIN should always
run read-only — but MySQL and MariaDB reject *every* statement inside a
`READ ONLY` transaction with error 1792, including a planning-only `EXPLAIN`
of a DML statement. Forcing `ReadOnly: true` unconditionally would break
`WithAllowDML` outright on those two dialects (this was tried in a review pass
and reverted for exactly this reason). The actual safety invariant — the
statement is never *executed* — is upheld by three other things working
together: `validate()`'s SELECT/WITH-only-by-default policy, never using
`EXPLAIN ANALYZE`, and the unconditional deferred `Rollback`. Don't "fix" the
`!dml` back to `true`; if you touch this, `TestMySQL_ExplainDMLDoesNotMutate`
is the test that would catch a real regression.

## Suppression uses two different regexes on purpose

`analyzer/suppress.go` has two suppression paths that look like they could
share a regex but must not: the in-SQL `-- sqlguard:ignore` / `/* sqlguard:
ignore:rule-a,rule-b */` form is parsed from raw SQL text with a
**marker-anchored** pattern specifically to avoid a string literal like
`'-- sqlguard:ignore'` inside a query being mistaken for a real directive. The
Go-source `// sqlguard:ignore[:rules]` form (`ParseIgnoreComment`) uses a
**separate, marker-less** pattern because `go/ast` has already stripped the
`//` by the time this code sees the comment text. Reusing one regex for the
other either breaks Go-source suppression (the marker-anchored form expects
the `//`/`/* */` delimiters still present) or reopens the string-literal false
positive in SQL.

## Redaction is a security invariant, not a formatting choice

Per `SECURITY.md`'s threat model, a `Result` carrying a raw literal value out
of the process is in scope as a security bug, not a correctness nit. `explain/`
is the one deliberate, documented exception (the user typed the query on their
own CLI; it never reaches a log/telemetry sink) — don't generalize that carve-
out to any other package, and don't flag `explain/` for keeping `Query` raw.

## Module topology

Nine Go modules (root, `parsers/pgparser`, `parsers/mysqlparser`, and six
`integrations/*`), all pinned to the same Go version and tied together at dev
time by the committed `go.work` (no `go.mod` here has a `replace`). A change to
an exported surface in `analyzer/` or `middleware/` needs the corresponding
change in every satellite that touches it, in the same PR — the root module's
own `go test ./...` does not reach `integrations/*` or `parsers/*`.
`test/integration/` is a further, unpublished module behind the `integration`
build tag, running `explain/` against live Postgres/MySQL/MariaDB — its pinned,
version-dependent tabular EXPLAIN output is exactly why it can't be replaced
with fixtures or mocks; don't suggest that.

## gosec is tuned, not disabled

`.golangci.yml` enables `gosec` (SQL injection, unhandled crypto/file ops) but
excludes only `G115` (integer-overflow-on-conversion), which is noisy against
this codebase's deliberate, range-checked `int`/`int64` conversions. The real
injection and file/crypto checks stay on. Don't read the `G115` exclusion as
license to relax other gosec findings, and don't suggest re-enabling `G115`
without also fixing the conversions it flags.

## Docs are versioned by snapshot, not per release

`website/docs/` is the unreleased documentation (served at `/docs/next/`);
`website/versioned_docs/version-X.Y/` is a frozen snapshot of a released
version (the newest one serves `/docs/`). A PR that changes documented
behaviour edits `website/docs/` and marks the version inline — `_0.3+_` in a
table cell, `_Added in 0.3._` opening a paragraph, `// 0.3+` in a code block,
`_Changed in 0.3._` plus a line on the old behaviour — rather than cutting a
new snapshot; snapshots are cut deliberately with `npm run cut-version` when
a release makes an existing page wrong for readers on the previous version
(`website/VERSIONING.md`). Never suggest editing a snapshot: it documents what
a shipped version does, and changing it rewrites history for users still on
it. The one thing worth being strict about in `website/docs/` is names — an
option like `WithFindingDedup` or a rule like `select-without-limit` written
wrong in the docs is a support burden, so check them against the source.
