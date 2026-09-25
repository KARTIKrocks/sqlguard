# CodeAnt AI configuration

Repository-level configuration for [CodeAnt AI](https://docs.codeant.ai),
checked in so the settings are reviewed like any other change instead of
living only in a dashboard. CodeAnt resolves configuration as **inline CI
parameters > this directory > dashboard settings**, and each level overrides
only the fields it defines.

This is the third reviewer configured for this repo, alongside
`.coderabbit.yaml` and `.greptile/`. The rule ids below deliberately match
`.greptile/config.json` where the rule is the same, so one invariant has one
name across every tool.

| File | Purpose | Reference |
| --- | --- | --- |
| `configuration.json` | Which analyses run, and over which files | [Analysis Configuration](https://docs.codeant.ai/repositories/analysis_configuration) |
| `review.json` | Repo-specific review rules, merged by `id` | [Rules](https://docs.codeant.ai/pull_request/customize/rules) |
| `instructions.json` | Context that prevents false positives, merged by `id` | [Instructions](https://docs.codeant.ai/pull_request/customize/instructions) |
| `quality_gates_conditions.json` | Conditions that gate a PR | [Quality Gates](https://docs.codeant.ai/pull_request/quality_gates/repository_configuration) |

Anything an organization-wide global config repo defines is merged in
underneath these files; a local `id` or `metric` always wins.

## What is enabled, and why

Two analyses are off. Both are switched off because CI already answers the
same question more accurately for this codebase, not because the question
does not matter:

- **`deadcode_analysis`** — this is a published library, so an exported
  identifier with no in-repo caller is the public API rather than dead code.
  `golangci-lint`'s `unused` runs in `make ci` and understands that
  distinction; a generic reachability pass would report the whole exported
  surface, plus the optional driver-interface methods in
  `middleware/driver.go` that exist so `database/sql`'s type assertions
  succeed.
- **`duplicatecode_analysis`** — the four `Query`/`Exec` branches in
  `middleware/driver.go` and the six `integrations/*` adapters are
  deliberately parallel; each branch forwards to a different optional base
  interface, and collapsing them changes fallback behaviour. AGENTS.md
  records this as an invariant.

Everything else stays on. `sast_analysis`, `secrets_analysis`, `sca_analysis`
and `iac_analysis` matter most here: sqlguard is a defensive security tool,
and `SECURITY.md` treats a missed dangerous query, a leaked literal or an
executed `EXPLAIN` as vulnerabilities rather than style nits.
`complex_function_analysis` keeps the default maintainability index of 15,
which pairs with `gocyclo`'s `min-complexity: 15` in `.golangci.yml`.

## File filters

Two separate keys, because they scope different things:

- `file_filters.config.exclude_files` scopes **analysis**. It drops build
  output, `node_modules`, the frozen docs snapshots and the static assets.
  `go.mod` and `go.sum` stay in scope — they are how software composition
  analysis sees the dependency graph.
- `review_configuration.exclude` scopes **PR review**, and additionally drops
  `go.sum`, `package-lock.json` and `testdata`, where a line-by-line AI
  comment has nothing useful to say.

`include_files` is left empty on purpose: when it is set it takes precedence
and the exclude patterns are ignored entirely.

`website/versioned_docs/**` and `website/versioned_sidebars/**` are excluded
from both. They are frozen release snapshots produced by
`npm run cut-version`; a suggestion there is unactionable by definition,
since editing a snapshot rewrites history for users still on that version.

## Quality gates

Security-first and deliberately small, so a red gate always means something:

| Metric | Condition | Effect |
| --- | --- | --- |
| `secrets` | `GREATER_THAN 0` | Any new secret fails the commit or PR |
| `sast_rating` | `LESS_THAN B` | Requires A or better (fails on a medium-or-worse finding) |
| `sca_rating` | `LESS_THAN B` | Same bar for dependency vulnerabilities |

The secrets condition excludes `test/integration/docker-compose.yml`, whose
user, password and database are all the literal string `sqlguard`. Those are
fixtures for ephemeral local containers on deliberately non-default host
ports, used by `make db-up`; they are not a credential for anything. The
exclusion is scoped to that one file so a real secret anywhere else still
fails the gate.

Coverage metrics (`new_coverage_percentage`, `total_coverage_percentage`) are
**not** configured. Coverage in this repo is merged by `make coverage` and
uploaded to Codecov (`codecov.yml`); CodeAnt would need its own
[coverage upload step](https://docs.codeant.ai/control_center/test_coverage/github)
in `ci.yml` plus an API token before such a gate could pass, and a gate that
cannot pass is worse than no gate. Add the upload first if you want one.

Auto-approval (`.codeant/approval.json`) is also not configured. `main` takes
changes through pull requests that a human merges, so nothing here should
approve itself.

## Changing this

- Rules and instructions are merged **by `id`** — keep ids stable and
  descriptive so a global config, or a future override, can address them.
- A rule says what the reviewer should *enforce*; an instruction gives context
  that stops it reporting something deliberate. Adding an instruction is
  usually the right fix for a false positive.
- `scope` must contain `"pr"` for a rule to apply during pull request review;
  `"ide"` applies it in the editor extensions.
- Prefer findings CI cannot already produce. `make ci` runs gofmt, `go vet`,
  `golangci-lint`, `govulncheck`, `go test -race` and markdownlint across all
  nine modules, and a separate workflow runs CodeQL.
