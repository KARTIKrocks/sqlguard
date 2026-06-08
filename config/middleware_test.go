package config

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KARTIKrocks/sqlguard"
	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/KARTIKrocks/sqlguard/reporter"

	_ "github.com/mattn/go-sqlite3"
)

func TestMiddlewareOptionsAppliesProfile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "rules:\n  disable: [select-star]\n")

	opts, err := Middleware("", dir)
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}

	var buf strings.Builder
	opts = append(opts, middleware.WithReporter(&reporter.ConsoleReporter{Out: &buf}))

	name := "sqlguard-cfg-test"
	if err := sqlguard.Register(name, "sqlite3", opts...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	db, err := sql.Open(name, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE u (id INTEGER, name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, err := db.Query("SELECT * FROM u WHERE id = 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if strings.Contains(buf.String(), "select-star") {
		t.Errorf("select-star should be disabled via config, got:\n%s", buf.String())
	}
}
