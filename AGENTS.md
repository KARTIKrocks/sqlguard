# AGENTS.md

This file provides guidance to AI coding agents (Claude Code and any other
agent that reads `AGENTS.md`) when working with code in this repository.

## Commands

Use the Makefile targets; `make help` lists them all. The non-obvious part is
that **every all-modules target loops over the nine modules** — `go test ./...`
(and `go build`/`go vet`/`go mod tidy`) from root does NOT reach `integrations/*`
or `parsers/*`, which are separate Go modules. The `MODULES` variable drives the
loop so a target can't silently skip a satellite.

- `make all` — `tidy fmt vet lint build test` across all modules.
- `make ci` — the CI pipeline: `fmt-check vet lint test-race`.
- `make build` — `go build ./...` in every module (compile check). `make cli` builds the `bin/sqlguard` binary; `make install` installs it.
- `make test` — `go test -count=1 ./...` in every module. `make test-race` adds `-race`; `make coverage` writes a merged `coverage.out`.
- `make lint` — `golangci-lint run` in every module (config in `.golangci.yml`, v2 schema). `make fmt` / `make fmt-check` run `gofmt -s` + `goimports`.
- `make tidy` — `go mod tidy` across all nine modules. Run after any dependency change; tidying only the root leaves the others stale.
- `make setup` — installs pinned `golangci-lint` / `goimports` if missing (a prereq of `lint`/`fmt`).
- `make release-prep VERSION=vX.Y.Z` — drops the local `replace` and pins each sub-module to the tagged parent version, then prints the per-module tag commands.

CI (`.github/workflows/ci.yml`) runs the root and each satellite module individually too.

Run a single test: `go test ./middleware/ -run TestDriver_QueryDetectsSelectStar -count=1`. Use `-race` for anything touching `middleware` (the driver chain and `QueryTracker` are concurrent): `go test -race ./middleware/`.

## Module topology

Nine Go modules, all on **Go 1.26**, kept in lockstep:

- root (`github.com/KARTIKrocks/sqlguard`) — core analyzer, middleware, reporter, `config`, CLI. Near-zero-dependency: `analyzer`/`middleware`/`reporter` stay dependency-free; the only third-party deps are sqlite3 (CLI `db`/tests), cobra (CLI), and `gopkg.in/yaml.v3` (isolated to the `config` package). Importing `analyzer`/`middleware` does not pull YAML.
- `parsers/pgparser`, `parsers/mysqlparser` — opt-in real SQL grammars, isolated in their own modules so the heavy parser deps never enter a consumer's build unless explicitly imported.
- `integrations/gormguard`, `integrations/sqlxguard`, `integrations/pgxguard`, `integrations/bunguard`, `integrations/xormguard`, `integrations/entguard` — ORM/driver adapters, also separate so their deps stay opt-in. **Every integration is now built on the exported `middleware.Guard` core**, so all inherit redaction-by-default, stable fingerprints, the parser seam, slow-query and N+1 with no parallel option surface. `pgxguard` covers native pgx/pgxpool (which bypasses `database/sql`) via pgx's tracer seam; `gormguard`/`bunguard`/`xormguard` hook each ORM's native before/after callback seam (`gorm.Plugin`, `bun.QueryHook`, xorm `contexts.Hook`); `entguard` decorates ent's `dialect.Driver` (Exec/Query + transactions). `gormguard`/`sqlxguard` were migrated to the shared core in roadmap item 11.1.

When changing the public API or Go version, update all nine `go.mod` files and `.github/workflows/ci.yml` together.

## Architecture

**Runtime interception is at the `database/sql` driver layer, not a wrapper type.** `middleware/driver.go` hand-implements the standard sqlmw/instrumentedsql/otelsql chain (`Driver`+`DriverContext` → `Connector` → `Conn` → `Stmt`/`Tx`) with zero dependencies. Consumers get a real `*sql.DB` back via `sqlguard.Register(name, baseDriver, opts...)` (then `sql.Open`) or `sqlguard.OpenDB(connector, opts...)`. Key invariant: optional driver interfaces (`QueryerContext`, `Pinger`, `SessionResetter`, `NamedValueChecker`, …) are _structurally_ implemented on every wrapper type, but each method forwards to the base only if the base implements that interface, otherwise returns `driver.ErrSkip` / a documented no-op so `database/sql` falls back exactly as it would for the bare driver. Do not "simplify" these away — they preserve base-driver behavior. The deprecated-path delegations (`base.Begin`, legacy `Queryer`/`Execer`, `Stmt.Exec/Query`) are deliberate and `//nolint:staticcheck`-annotated; a faithful wrapper must delegate to whatever the wrapped driver exposes.

**`middleware/guard.go`** is the single analysis core, exported as `middleware.Guard` with `Check` / `CheckLatency` / `Observe` (start-end split, returns a latency closure designed for ctx-stashed tracer hooks like pgx) / `ResetN1` / `Analyzer`. Every interception point in the driver chain (`driver.go`) calls into one `Guard`, and **every out-of-tree integration must too** — `integrations/pgxguard` is the reference example. Hand-rolling `check`/`checkLatency` (the old `sqlxguard`/`gormguard` pattern) silently loses redaction-by-default, fingerprints, the parser seam, config, and N+1; do not copy that shape for new integrations.

**The analyzer is parser-pluggable.** `analyzer.Analyzer` runs `Rule`s against a normalized, dialect-agnostic `Statement` produced by an `analyzer.Parser` (`analyzer/parser.go`, `statement.go`). The default `FallbackParser` (`fallback.go`) is zero-dependency, strips comments/string literals, and never errors. `analyzer.Analyze` degrades to the FallbackParser if a configured parser errors, so analysis never breaks the caller's query path. Real grammars are supplied via `middleware.WithParser(...)` / `analyzer.Default().WithParser(...)` using the `parsers/*` modules. Rules read the `Statement`, never raw SQL.

**Rules self-register; config is resolved once, never per query.** Built-in rules call `analyzer.Register(RuleSpec{...})` from `init()` in `analyzer/rules.go` — a stable name, default severity, and a settings-aware `Factory`. To add a rule, write it and add one `Register` call; **do not** hand-maintain a rule list in `Default()`. Being addressable by name is what makes enable/disable, severity overrides, per-rule `Settings`, and suppressions work uniformly. `analyzer.Profile` (disabled set, `only` whitelist, severity map, per-rule settings) is applied in `DefaultWithProfile` at construction; the per-query `Analyze` path does no config work (it runs on every query through the driver — keep it allocation-light). `analyzer` must stay free of `config`/YAML imports.

**`config` is the only YAML-aware package.** It loads `.sqlguard.yml` (`Load`/`Discover` walks up to the git root), translates it to an `analyzer.Profile`, and exposes `MiddlewareOptions()`/`Middleware()` helpers. It depends on `analyzer` (and `middleware` for the helper); nothing depends on `config`. This keeps `gopkg.in/yaml.v3` out of the `analyzer`/`middleware` import graph for library users who don't opt into file config. Parsing is lenient by default (unknown keys/rules warn); `strict: true` makes them fatal — so a newer config still loads on an older binary.

**Suppression has two layers** (`analyzer/suppress.go`): in-SQL `-- sqlguard:ignore` / `/* sqlguard:ignore:rule-a,rule-b */`, parsed from raw SQL with a **marker-anchored** regex (avoids string-literal false positives), honored at runtime _and_ statically; and Go-source `// sqlguard:ignore[:rules]` via `ParseIgnoreComment`, which uses a **separate marker-less** regex because go/ast already strips the `//`. The scanner (`cmd/sqlguard/scan.go`) applies it against the AST comment map for the call line and the line directly above.

**All entry surfaces share the analyzer/reporter.** In-repo: the runtime `database/sql` driver chain (above), the CLI static scanner (`cmd/sqlguard/scan.go`), and the EXPLAIN-plan analyzer (`explain/`). Out-of-tree integrations are additional runtime entry surfaces and go through the same exported `middleware.Guard` (`pgxguard` hooks `pgx.QueryTracer`/`BatchTracer` for native pgx/pgxpool, which never touches `database/sql`). Findings are `analyzer.Result`s emitted through a `reporter.Reporter` (default `reporter.ConsoleReporter`).

**Redaction is the default, and there is one canonical normalizer.** `analyzer/redact.go` holds `Redact` (single-quoted string + numeric literals → `?`, comments stripped, identifiers/structure preserved — reuses the FallbackParser's comment/literal lexer, never errors) and `Fingerprint` (`Redact` + whitespace-collapse + `(?, ?, ?)`→`(?)` list-fold). `Result.Query` is redacted **before any Result leaves the process** so literals never reach a log sink; `Result.Fingerprint` is **always** set (PII-free, low-cardinality, safe as a metric label). Policy lives on the `Analyzer` (`rawQuery` field, default redact; `WithRawQuery()` opt-out; `Profile.RawQuery` / config `redact: false`); `Analyze` sets `Query`/`Fingerprint` on every result centrally, and direct-built findings (slow-query in `guard.go`, n+1) go through `Analyzer.PrepareQuery`. **Do not add a second normalizer** — `middleware.normalizeQuery` delegates to `analyzer.Fingerprint`; the N+1 group key _is_ the fingerprint. `explain` keeps `Query` raw (the user typed it on their own CLI) but still sets `Fingerprint`.

**`explain` is hostile-input-validated, never executes.** `explain.validate` rejects empty input, uses `analyzer.IsMultiStatement` (comment/string-literal-aware — a `;` in a `--`/`/* */`/string can't smuggle a second statement) instead of `strings.Contains(query, ";")`, and classifies via the FallbackParser: `SELECT`/`WITH` only by default, DML behind `explain.WithAllowDML()` (CLI `--allow-dml`), everything else refused. Every EXPLAIN runs in a `BeginTx(ReadOnly:true)` + deferred `Rollback` (Postgres _and_ MySQL) and never uses `ANALYZE`, so it is plan-only and cannot commit. EXPLAIN takes no bind params, so concatenation is unavoidable by design — the defense is `validate` + the rolled-back read-only tx, not parameterization; keep both.

## Conventions

- **Pre-release, no backward compatibility.** Nothing is shipped. Prefer the clean design over preserving existing public APIs; do not add deprecation shims or compat layers. When a better design presents itself, replace rather than add-alongside.
- Modern Go idioms expected (range-over-int, compile-time interface-satisfaction asserts `var _ I = (*T)(nil)`, `any`).
- Lint config specifics: `revive`'s `exported` rule is **enabled** — exported symbols need a doc comment (starting with the symbol name), and type names must not stutter with their package (e.g. `explain.Result`, not `explain.ExplainResult`); `gocyclo` min-complexity is 15; `errcheck` is relaxed in `_test.go`.
