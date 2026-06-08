// Package sqlguard is a production-safe SQL query analyzer for Go applications.
//
// It detects slow queries, dangerous SQL patterns, and performance issues
// both at runtime (via a database/sql driver wrapper) and statically
// (via the CLI).
//
// The runtime guard wraps at the driver.Driver layer, so it returns a real
// *sql.DB and analyzes every query — including those issued by ORMs and
// query builders — without a method list to keep in sync.
//
// Register a wrapped driver by name:
//
//	sqlguard.Register("sqlguard-pg", "pgx")
//	db, _ := sql.Open("sqlguard-pg", dsn)
//	db.Query("SELECT * FROM users") // logs warning about SELECT *
//
// Or wrap an existing driver.Connector directly:
//
//	db := sqlguard.OpenDB(connector)
package sqlguard

import (
	"database/sql"
	"database/sql/driver"

	"github.com/KARTIKrocks/sqlguard/middleware"
)

// Register wraps the database/sql driver registered under baseDriver and
// registers the analyzed result under name. Afterwards sql.Open(name, dsn)
// yields a *sql.DB whose every query is analyzed.
func Register(name, baseDriver string, opts ...middleware.Option) error {
	return middleware.Register(name, baseDriver, opts...)
}

// OpenDB wraps a driver.Connector and returns an analyzed *sql.DB. Use this
// when you already hold a connector (e.g. pgx's stdlib.GetConnector).
func OpenDB(c driver.Connector, opts ...middleware.Option) *sql.DB {
	return middleware.OpenDB(c, opts...)
}
