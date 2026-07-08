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

The satellite modules use a local `replace` directive pointing at the root, so
you can develop across modules without publishing.

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
