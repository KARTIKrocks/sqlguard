module github.com/KARTIKrocks/sqlguard/integrations/sqlxguard

go 1.26

require (
	github.com/KARTIKrocks/sqlguard v0.0.0
	github.com/jmoiron/sqlx v1.4.0
)

require github.com/mattn/go-sqlite3 v1.14.45

replace github.com/KARTIKrocks/sqlguard => ../..
