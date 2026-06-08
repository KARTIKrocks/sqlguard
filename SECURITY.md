# Security Policy

## Supported versions

sqlguard is pre-1.0. Security fixes are applied to the latest released minor
version. Until 1.0, only the most recent `0.x` release is supported.

| Version      | Supported |
| ------------ | --------- |
| latest `0.x` | ✅        |
| older        | ❌        |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, use one of the following private channels:

1. **GitHub private vulnerability reporting** (preferred) — open a report via
   the repository's **Security → Report a vulnerability** tab
   (`https://github.com/KARTIKrocks/sqlguard/security/advisories/new`).
2. **Email** — `kartik.rajput622001@gmail.com` with a subject line starting
   `[sqlguard security]`.

Please include:

- the affected module(s) and version/commit,
- a description of the issue and its impact,
- steps to reproduce (a minimal repro or PoC is ideal),
- any suggested remediation.

You can expect an acknowledgement within **5 business days**. We'll keep you
informed as we investigate and work on a fix, and we'll credit you in the
release notes / advisory unless you prefer to remain anonymous.

## Scope and threat model

sqlguard is a _defensive_ tool — it analyzes SQL for risky patterns and is
designed to fail safe. A few invariants are part of its security contract; bugs
that break them are in scope:

- **Redaction by default.** A `Result` must never carry raw literal values out
  of the process: `Result.Query` is redacted and `Result.Fingerprint` must be
  PII-free. A path that leaks literals to a reporter/log is a security bug.
- **EXPLAIN never executes the statement.** The `explain` analyzer validates
  input (comment/string-aware multi-statement rejection, `SELECT`/`WITH`-only by
  default) and runs every plan inside an always-rolled-back, read-only
  transaction. A way to make `explain` mutate data or run a second statement is
  in scope.
- **The middleware must not alter query semantics or results.** It observes;
  it must not change what the underlying driver returns.

Out of scope: vulnerabilities in third-party dependencies (report those
upstream; we'll bump once fixed), and misuse such as deliberately disabling
redaction with `WithRawQuery()` / `redact: false`.
