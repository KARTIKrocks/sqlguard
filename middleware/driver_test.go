package middleware

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/reporter"

	_ "github.com/mattn/go-sqlite3"
)

var driverSeq atomic.Int64

// newGuardedDB registers a uniquely-named wrapped sqlite3 driver with the
// given options and returns an analyzed *sql.DB backed by a temp-file
// database (so the connection pool sees a consistent schema).
func newGuardedDB(t *testing.T, opts ...Option) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("sqlguard-test-%d", driverSeq.Add(1))
	if err := Register(name, "sqlite3", opts...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO users (name, email) VALUES ('alice', 'alice@example.com')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return db
}

func guardedWithBuffer(t *testing.T, extra ...Option) (*sql.DB, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	opts := append([]Option{WithReporter(reporter.NewConsoleReporterTo(&buf))}, extra...)
	return newGuardedDB(t, opts...), &buf
}

func TestDriver_ReturnsRealSQLDB(t *testing.T) {
	db, _ := guardedWithBuffer(t)
	// The whole point: Register/sql.Open yield a real *sql.DB, usable
	// anywhere one is expected (no wrapper type to thread through).
	if db == nil {
		t.Fatal("expected a *sql.DB")
	}
}

func TestDriver_QueryDetectsSelectStar(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	rows, err := db.Query("SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if !strings.Contains(buf.String(), "select-star") {
		t.Errorf("expected select-star warning, got: %q", buf.String())
	}
}

func TestDriver_NoWarningForSafeQuery(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	rows, err := db.Query("SELECT id, name FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if buf.Len() != 0 {
		t.Errorf("expected no warnings, got: %q", buf.String())
	}
}

func TestDriver_ExecDetectsDeleteWithoutWhere(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	if _, err := db.Exec("DELETE FROM users"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if !strings.Contains(buf.String(), "delete-without-where") {
		t.Errorf("expected delete-without-where, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "CRITICAL") {
		t.Error("expected CRITICAL severity")
	}
}

func TestDriver_QueryRowDetectsLeadingWildcard(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	_ = db.QueryRow("SELECT id FROM users WHERE email LIKE '%gmail%'")

	if !strings.Contains(buf.String(), "leading-wildcard") {
		t.Errorf("expected leading-wildcard, got: %q", buf.String())
	}
}

func TestDriver_PreparedStatementIsAnalyzed(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	stmt, err := db.Prepare("SELECT * FROM users")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query()
	if err != nil {
		t.Fatalf("stmt query: %v", err)
	}
	rows.Close()

	if !strings.Contains(buf.String(), "select-star") {
		t.Errorf("expected select-star on prepared exec, got: %q", buf.String())
	}
}

func TestDriver_TransactionIsAnalyzed(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("DELETE FROM users"); err != nil {
		t.Fatalf("tx exec: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if !strings.Contains(buf.String(), "delete-without-where") {
		t.Errorf("expected delete-without-where in tx, got: %q", buf.String())
	}
}

func TestDriver_TransactionCommitRollback(t *testing.T) {
	db, _ := guardedWithBuffer(t)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO users (name, email) VALUES (?, ?)", "bob", "bob@example.com"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows after commit, got %d", count)
	}

	tx2, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx2.Exec("DELETE FROM users WHERE name = ?", "bob"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows after rollback, got %d", count)
	}
}

func TestDriver_SlowQueryDetection(t *testing.T) {
	db, buf := guardedWithBuffer(t, WithSlowQueryThreshold(1*time.Nanosecond))

	rows, err := db.Query("SELECT id FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if !strings.Contains(buf.String(), "slow-query") {
		t.Errorf("expected slow-query with 1ns threshold, got: %q", buf.String())
	}
}

func TestDriver_NoSlowQueryBelowThreshold(t *testing.T) {
	db, buf := guardedWithBuffer(t, WithSlowQueryThreshold(1*time.Hour))

	rows, err := db.Query("SELECT id FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if strings.Contains(buf.String(), "slow-query") {
		t.Errorf("did not expect slow-query, got: %q", buf.String())
	}
}

func TestDriver_CustomAnalyzer(t *testing.T) {
	db, buf := guardedWithBuffer(t, WithAnalyzer(analyzer.New(analyzer.CheckDeleteWithoutWhere)))

	rows, err := db.Query("SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if strings.Contains(buf.String(), "select-star") {
		t.Errorf("did not expect select-star with custom analyzer, got: %q", buf.String())
	}
}

func TestDriver_N1Detection(t *testing.T) {
	db, buf := guardedWithBuffer(t, WithN1Detection(3, time.Second))

	for i := range 5 {
		row := db.QueryRow("SELECT name FROM users WHERE id = ?", i)
		var name string
		_ = row.Scan(&name)
	}

	if !strings.Contains(buf.String(), "n-plus-one") {
		t.Errorf("expected n-plus-one warning, got: %q", buf.String())
	}
}

func TestRegister_DuplicateNameErrors(t *testing.T) {
	name := fmt.Sprintf("sqlguard-dup-%d", driverSeq.Add(1))
	if err := Register(name, "sqlite3"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := Register(name, "sqlite3"); err == nil {
		t.Error("expected error registering duplicate name")
	}
}

func TestRegister_UnknownBaseDriverErrors(t *testing.T) {
	if err := Register("sqlguard-x", "no-such-driver"); err == nil {
		t.Error("expected error for unknown base driver")
	}
}
