//go:build integration

// Package integration exercises explain.PlanAnalyzer against live databases.
//
// These tests are excluded from a normal `go test ./...` by the build tag.
// Bring the servers up and run them with:
//
//	docker compose -f test/integration/docker-compose.yml up -d --wait
//	make test-integration
//
// Each test skips when its DSN environment variable is unset, so a partial
// stack (say, Postgres only) still runs the tests it can.
package integration

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/explain"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// DSN environment variables. The defaults in docker-compose.yml are:
//
//	SQLGUARD_TEST_PG_DSN      postgres://sqlguard:sqlguard@localhost:55432/sqlguard?sslmode=disable
//	SQLGUARD_TEST_MYSQL_DSN   root:sqlguard@tcp(localhost:53306)/sqlguard
//	SQLGUARD_TEST_MARIADB_DSN root:sqlguard@tcp(localhost:53307)/sqlguard
const (
	envPostgres = "SQLGUARD_TEST_PG_DSN"
	envMySQL    = "SQLGUARD_TEST_MYSQL_DSN"
	envMariaDB  = "SQLGUARD_TEST_MARIADB_DSN"
)

// connect opens the database named by env, skipping the test when it is unset.
// driver is the database/sql driver name ("pgx" or "mysql").
func connect(t *testing.T, env, driver string) *sql.DB {
	t.Helper()

	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s not set; start the stack with `docker compose -f test/integration/docker-compose.yml up -d --wait`", env)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", driver, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping %s: %v", driver, err)
	}
	return db
}

// exec runs each statement in order, failing the test on the first error.
func exec(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

// testCtx returns a context that expires well before the Go test timeout, so a
// hung server surfaces as a test failure rather than a panic.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ruleNames returns the RuleName of every issue, in order.
func ruleNames(issues []analyzer.Result) []string {
	names := make([]string, len(issues))
	for i, r := range issues {
		names[i] = r.RuleName
	}
	return names
}

// hasRule reports whether any issue carries the given rule name.
func hasRule(issues []analyzer.Result, name string) bool {
	for _, r := range issues {
		if r.RuleName == name {
			return true
		}
	}
	return false
}

// analyzerFor builds a PlanAnalyzer, failing the test if construction errors.
func analyzerFor(t *testing.T, db *sql.DB, dialect string, opts ...explain.Option) *explain.PlanAnalyzer {
	t.Helper()
	p, err := explain.New(db, dialect, opts...)
	if err != nil {
		t.Fatalf("explain.New(%q): %v", dialect, err)
	}
	return p
}
