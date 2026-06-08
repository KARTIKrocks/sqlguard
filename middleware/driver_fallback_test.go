package middleware

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"testing"
	"time"
)

// fakeNoQueryerDriver is a minimal driver whose Conn implements neither
// QueryerContext/ExecerContext nor the legacy Queryer/Execer. database/sql is
// therefore forced down its Prepare+Stmt fallback path for every Query/Exec —
// the path where wConn.{Query,Exec}Context return driver.ErrSkip. It exists to
// prove a single logical query is analyzed exactly once even then.
type fakeNoQueryerDriver struct{}

func (fakeNoQueryerDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (*fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (*fakeConn) Close() error                        { return nil }
func (*fakeConn) Begin() (driver.Tx, error)           { return &fakeTx{}, nil }

type fakeStmt struct{}

func (*fakeStmt) Close() error                               { return nil }
func (*fakeStmt) NumInput() int                              { return -1 } // skip arg-count checking
func (*fakeStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (*fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return &fakeRows{}, nil }

type fakeRows struct{}

func (*fakeRows) Columns() []string         { return nil }
func (*fakeRows) Close() error              { return nil }
func (*fakeRows) Next([]driver.Value) error { return io.EOF }

type fakeTx struct{}

func (*fakeTx) Commit() error   { return nil }
func (*fakeTx) Rollback() error { return nil }

// openFakeGuarded registers a wrapped fakeNoQueryerDriver and returns the DB
// plus the reporter that records findings. Dedup is off so every analysis is
// counted (the bug would surface as 2 findings for one query).
func openFakeGuarded(t *testing.T) (*sql.DB, *countingReporter) {
	t.Helper()
	rep := &countingReporter{}
	name := fmt.Sprintf("sqlguard-fake-%d", driverSeq.Add(1))
	sql.Register(name, WrapDriver(fakeNoQueryerDriver{}, WithReporter(rep), WithFindingDedup(0)))
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, rep
}

func TestDriver_NoQueryerContextAnalyzedOnce(t *testing.T) {
	db, rep := openFakeGuarded(t)

	rows, err := db.Query("DELETE FROM accounts") // flagged: delete-without-where
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := rep.count(); got != 1 {
		t.Errorf("expected one logical query analyzed once via the prepare fallback, got %d", got)
	}
}

func TestDriver_NoExecerContextAnalyzedOnce(t *testing.T) {
	db, rep := openFakeGuarded(t)

	if _, err := db.Exec("DELETE FROM accounts"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if got := rep.count(); got != 1 {
		t.Errorf("expected one logical query analyzed once via the prepare fallback, got %d", got)
	}
}

// With N+1 enabled, each logical query must increment the counter once. If the
// ErrSkip path double-counted, threshold=2 would trip after a single query.
func TestDriver_NoQueryerContextN1CountedOnce(t *testing.T) {
	rep := &countingReporter{}
	name := fmt.Sprintf("sqlguard-fake-%d", driverSeq.Add(1))
	sql.Register(name, WrapDriver(fakeNoQueryerDriver{},
		WithReporter(rep), WithFindingDedup(0), WithN1Detection(2, time.Minute)))
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// One execution of a non-flagged query: no static finding, and the N+1
	// counter should be at 1 (below threshold 2), so nothing is reported.
	rows, err := db.Query("SELECT id, name FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := rep.count(); got != 0 {
		t.Errorf("one logical query must not trip N+1 (threshold 2); got %d reports", got)
	}
}
