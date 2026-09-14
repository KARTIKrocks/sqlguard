// Integration tests for the explain/ package against live databases.
//
// This module is never published or tagged: it exists so the Postgres and
// MySQL drivers stay out of the core module's import graph. Like every module
// here it carries no `replace` directive — the root go.work points it at the
// working tree.
module github.com/KARTIKrocks/sqlguard/test/integration

go 1.27

require (
	github.com/KARTIKrocks/sqlguard v0.1.0
	github.com/go-sql-driver/mysql v1.10.1
	github.com/jackc/pgx/v5 v5.11.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
