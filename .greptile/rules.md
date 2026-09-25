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

## The literal lexers scan twice on purpose — worked example

`Redact` and `IsMultiStatement` each scan SQL literals under two dialect
readings, and each takes the **opposite** fail-safe direction. Collapsing
either to a single pass is a security bug. All three of the following were
introduced and fixed during PR #61, so treat a "simplification" here with
suspicion:

1. **Redact honoured only `''` escapes.** A literal containing `\'` — the
   default escape on MySQL and in Postgres `E'…'` — closed at the wrong quote,
   desynchronising the lexer so it emitted the *following* literal verbatim:
   `E'it\'s' AND token = 'sk-live-abcdef'` redacted to
   `E?s?sk-live-abcdef?`. `Redact` now scans both backslash readings and
   redacts the **union**; over-redaction is a readability cost, under-redaction
   is a leak.

2. **`stripComments` and `blankLiterals` disagreed about dollar quotes.**
   `stripComments` copies a dollar body verbatim (a `--` inside one is data),
   so a quote in that body reached a blanker that read it as an ordinary
   literal and blanked everything after it — separator included.
   `IsMultiStatement("SELECT $$'$$; DROP TABLE t")` returned false. The two
   lexers must agree.

3. **One `$$` reading is never enough.** Reading `$tag$…$tag$` as a literal
   hides the `;` in `UPDATE t AS $$ SET id = 1; DROP TABLE t` behind an
   unterminated body; reading `$$` as ordinary bytes hides the `;` in
   `SELECT $$'$$; DROP TABLE t` behind an unterminated ordinary literal. Each
   reading is blind to what the other catches, so `IsMultiStatement` runs both
   and refuses if **either** sees a separator.

Note that `IsMultiStatement` lives in `analyzer/` but is `explain`'s
stacked-statement guard, and the MySQL `--allow-dml` path runs read-write
where DDL implicit-commits past the rollback. A change in `analyzer/` can
therefore be an EXPLAIN bypass. `TestRedactNoLeakAcrossDialectAmbiguity` and
`TestIsMultiStatementNeedsBothReadings` pin all of this; a change that deletes
either test needs to justify itself.

## One execution, one analysis — worked example

`analyze-once-per-execution` exists because `database/sql` re-issues a query
more often than the wrapper's shape suggests, and the original code analyzed
before handing the query to the base driver. That was correct only for a base
with no direct `Queryer`/`Execer` at all, which the comment there addressed.
Two other answers mean "this did not run", and both re-enter the chain:

- `driver.ErrSkip` is a **per-call** answer, not only a per-driver one. A base
  that implements `QueryerContext` may still decline an individual query, and
  `database/sql` then falls back to Prepare+Query, which re-enters through
  `wStmt` and analyzes there. `go-sql-driver/mysql` answers `ErrSkip` for
  every parameterized query unless `interpolateParams=true`, which is off by
  default — so on MySQL essentially all application traffic was analyzed
  twice (issue #67).
- `driver.ErrBadConn` means the connection was already dead. Its contract
  forbids returning it when the operation may have been performed, so nothing
  executed, and `database/sql` retries the whole query on another connection —
  twice from the pool, then once on a fresh one. A stale pool (MySQL's
  `wait_timeout`, a restart, a failover) therefore produced **three** analyses
  for one logical query.

The visible damage is not the duplicate static finding — that hides behind the
default one-minute dedup window, which is why the bug went unnoticed. It is
the N+1 counter, which is not deduped: `WithN1Detection(10, …)` fired at five
real queries on MySQL, so every configured threshold was silently halved.

The fix lands in #67, as one shape applied at every interception point in
`driver.go`: call the base, then `analyzeExecuted`, which returns without
analyzing when the answer is `ErrSkip` or `ErrBadConn`. Two consequences are deliberate. Analysis
happens after execution, which is fine because nothing consumes findings
before the query runs; and the latency window is read before the rules run, so
analysis time cannot push a query past the slow-query threshold. Argument
conversion also moved ahead of analysis, so a call rejected with
"driver does not support named parameters" — which never reaches the database
— is no longer counted.

`Guard.Observe` (check, then time) survives for interception points that are
only ever told a query ran: the out-of-tree integrations. Nothing in
`driver.go` is in that position, so restoring it there reintroduces the bug.
Its regression tests are fake drivers rather than assertions on internals —
`fakeErrSkipDriver` and `fakeBadConnDriver` in
`middleware/driver_fallback_test.go` — and each fails against the unfixed code
with an exact count (2, 3, or a tripped N+1 threshold).

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
