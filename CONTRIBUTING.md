# Contributing to sqlguard

Thanks for your interest in improving sqlguard! This guide covers the
project-specific things that aren't obvious from a quick look at the repo.

## Project layout

sqlguard is a **multi-module repo** — nine Go modules on Go 1.26, kept in
lockstep:

- **root** (`github.com/KARTIKrocks/sqlguard`) — core analyzer, middleware,
  reporter, config, and CLI. Deliberately near-zero-dependency.
- **`parsers/pgparser`, `parsers/mysqlparser`** — opt-in real SQL grammars,
  isolated so their heavy parser deps never enter a consumer's build.
- **`integrations/{gormguard,sqlxguard,pgxguard,bunguard,xormguard,entguard}`** —
  ORM/driver adapters, each a separate module so its deps stay opt-in.

Every satellite `require`s a published version of the core. The committed
`go.work` at the repo root overrides that with the working tree, so you can
develop across modules without publishing — and so a breaking change to
`analyzer/` or `middleware/` fails the satellite tests instead of passing CI
against a stale published core.

No `go.mod` in this repo carries a `replace` directive; `go.work` is the only
place local resolution is configured. It is ignored entirely when someone
depends on these modules. To reproduce a consumer's build, set `GOWORK=off`.

> **Important:** `go test ./...` (and `go build` / `go vet` / `go mod tidy`)
> from the root does **not** reach the satellite modules. Always use the
> Makefile targets, which loop over every module.

## Development workflow

```bash
make setup      # install pinned golangci-lint + goimports (one-time)
make all        # tidy, fmt, vet, lint, build, test across all nine modules
make ci         # what CI runs: fmt-check, vet, lint, test-race
make test-race  # race detector (required for anything touching middleware)
make help       # list every target
```

Before opening a PR, run `make ci` and make sure it's green.

- Run a single test: `go test ./middleware/ -run TestName -count=1`.
- Use `-race` for anything touching `middleware` (the driver chain and
  `QueryTracker` are concurrent).
- After any dependency change, run `make tidy` (tidies all nine modules — tidying
  only the root leaves the others stale).

## Integration tests

`explain/` parses real `EXPLAIN` output, whose shape varies by server and
version — MySQL 9 defaults `@@explain_format` to `TREE`, and MariaDB emits ten
plan columns where MySQL emits twelve. No unit test can catch a regression
there, so `test/integration/` runs the analyzer against live databases:

```bash
make db-up            # postgres 18, mysql 9.7, mariadb 12.3 via docker compose
make test-integration
make db-down          # stop and drop the volumes
```

The tests sit behind the `integration` build tag, so `make test` and `make ci`
never need Docker. Each database's tests skip when its DSN environment variable
is unset (`SQLGUARD_TEST_PG_DSN`, `SQLGUARD_TEST_MYSQL_DSN`,
`SQLGUARD_TEST_MARIADB_DSN`), so a partial stack still runs what it can — and
you can point any of them at your own server by overriding the variable.

`test/integration/` is a tenth module that is never published: it exists to keep
the Postgres and MySQL drivers out of the core module's import graph.

## Releasing

Tag the root module first, then point each satellite at that tag. Everything
here is safe to commit to `main` — `go.work` keeps local builds on the working
tree no matter which core version the satellites require.

```bash
git tag vX.Y.Z && git push origin vX.Y.Z    # root module first

for mod in integrations/* parsers/*; do
  (cd "$mod" && go mod edit -require github.com/KARTIKrocks/sqlguard@vX.Y.Z)
done
make tidy && make test

git commit -am 'Pin sub-modules to vX.Y.Z'
for mod in integrations/* parsers/*; do git tag "$mod/vX.Y.Z"; done
git push origin --tags
```

Skip `test/integration/` — it is not published. Before tagging, it is worth
running `GOWORK=off make test` once: that is what a consumer's build sees, and
it will catch a satellite whose required core version predates an API it uses.

## Conventions

- **Pre-1.0, no backward-compatibility burden.** Prefer the clean design over
  preserving an existing public API; don't add deprecation shims or compat
  layers.
- Modern Go idioms are expected (range-over-int, `any`, compile-time interface
  asserts `var _ I = (*T)(nil)`).
- Keep the **core dependency-light**: `analyzer`, `middleware`, and `reporter`
  must stay free of third-party deps and of YAML. `config` is the only
  YAML-aware package.
- **Redaction is the default.** Never let raw literal values reach a `Result`
  that leaves the process. There is one canonical normalizer (`analyzer.Redact`
  / `Fingerprint`) — don't add a second.
- See [`AGENTS.md`](AGENTS.md) for the deeper architecture notes and invariants.

## Adding a detection rule

Rules self-register. Write the rule, then add one `analyzer.Register(RuleSpec{
... })` call in `analyzer/rules.go` (a stable name, default severity, and a
settings-aware factory). Being addressable by name is what makes enable/disable,
severity overrides, per-rule settings, and suppressions all work uniformly —
do **not** hand-maintain a rule list. Rules read the normalized `Statement`,
never raw SQL.

If your rule has a tunable, read it from `Settings` in the factory and document
it in [`.sqlguard.example.yml`](.sqlguard.example.yml).

## Adding an integration

Every integration must build on the exported `middleware.Guard` core —
`integrations/pgxguard` is the reference. Hand-rolling analysis silently loses
redaction, fingerprints, the parser seam, config, N+1, and dedup. Each
integration should expose `ResetN1()` for per-request scoping.

## Pull requests

1. Fork and branch from `main`.
2. Keep changes focused; update docs (`README.md`, `AGENTS.md`,
   `.sqlguard.example.yml`) when behavior or config changes.
3. Add tests for new behavior; where practical, also prove the failure mode
   (e.g. a bug-reintroduction check).
4. Add a line under `## [Unreleased]` in [`CHANGELOG.md`](CHANGELOG.md).
5. Run `make ci` and ensure it passes.

## Reporting security issues

Please do **not** open a public issue for security vulnerabilities. See
[`SECURITY.md`](SECURITY.md) for the private reporting process.

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
