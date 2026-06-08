package main

import (
	"database/sql"
	"fmt"
)

// openDB opens a database connection using the appropriate driver.
func openDB(dialect, dsn string) (*sql.DB, error) {
	var driverName string
	switch dialect {
	case "postgres":
		driverName = "postgres"
	case "mysql":
		driverName = "mysql"
	default:
		return nil, fmt.Errorf("unsupported dialect: %s", dialect)
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cannot reach database: %w", err)
	}

	return db, nil
}
