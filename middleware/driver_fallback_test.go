package middleware

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
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

// fakeErrSkipDriver mirrors go-sql-driver/mysql: its Conn *does* implement
// QueryerContext/ExecerContext, but declines every query with driver.ErrSkip
// (mysql does exactly that for parameterized queries unless
// interpolateParams=true, which is off by default). database/sql then falls
// back to Prepare+Query, re-entering through wStmt, so the query must still
// be analyzed exactly once.
type fakeErrSkipDriver struct{}

func (fakeErrSkipDriver) Open(string) (driver.Conn, error) { return &errSkipConn{}, nil }

type errSkipConn struct{ fakeConn }

func (*errSkipConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, driver.ErrSkip
}

func (*errSkipConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, driver.ErrSkip
}

// fakeQueryerDriver answers on the direct path: its Conn executes the query
// itself, so database/sql never falls back to Prepare. Analysis happens at
// that level instead — still exactly once.
type fakeQueryerDriver struct{}

func (fakeQueryerDriver) Open(string) (driver.Conn, error) { return &queryerConn{}, nil }

type queryerConn struct{ fakeConn }

func (*queryerConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{}, nil
}

func (*queryerConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

// fakeBadConnDriver fails the first two executions with driver.ErrBadConn,
// as a driver does when the pool hands out a connection the server has since
// closed (MySQL's wait_timeout, a restart, a failover). database/sql retries
// the whole query on a fresh connection, re-entering the wrapper — and by
// ErrBadConn's contract the declined attempts executed nothing, so they must
// not be analyzed.
type fakeBadConnDriver struct{ fails atomic.Int64 }

func (d *fakeBadConnDriver) Open(string) (driver.Conn, error) { return &badConn{d: d}, nil }

type badConn struct {
	fakeConn
	d *fakeBadConnDriver
}

func (c *badConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.d.fails.Add(-1) >= 0 {
		return nil, driver.ErrBadConn
	}
	return &fakeRows{}, nil
}

func (c *badConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if c.d.fails.Add(-1) >= 0 {
		return nil, driver.ErrBadConn
	}
	return driver.RowsAffected(0), nil
}

// openFakeGuarded registers base wrapped in a Guard and returns the DB plus
// the reporter that records findings. Dedup is off so every analysis is
// counted (a double analysis surfaces as 2 findings for one query).
func openFakeGuarded(t *testing.T, base driver.Driver, extra ...Option) (*sql.DB, *countingReporter) {
	t.Helper()
	rep := &countingReporter{}
	name := fmt.Sprintf("sqlguard-fake-%d", driverSeq.Add(1))
	opts := append([]Option{WithReporter(rep), WithFindingDedup(0)}, extra...)
	sql.Register(name, WrapDriver(base, opts...))
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, rep
}

func TestDriver_NoQueryerContextAnalyzedOnce(t *testing.T) {
	db, rep := openFakeGuarded(t, fakeNoQueryerDriver{})

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
	db, rep := openFakeGuarded(t, fakeNoQueryerDriver{})

	if _, err := db.Exec("DELETE FROM accounts"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if got := rep.count(); got != 1 {
		t.Errorf("expected one logical query analyzed once via the prepare fallback, got %d", got)
	}
}

// With N+1 enabled, each logical query must increment the counter once. If a
// fallback path double-counted, threshold=2 would trip after a single query.
func TestDriver_NoQueryerContextN1CountedOnce(t *testing.T) {
	assertOneQueryDoesNotTripN1(t, fakeNoQueryerDriver{})
}

// The ErrSkip path is the one that matters in production: on MySQL every
// parameterized query takes it, so a double count halves every configured
// N+1 threshold (issue #67).
func TestDriver_ErrSkipQueryAnalyzedOnce(t *testing.T) {
	db, rep := openFakeGuarded(t, fakeErrSkipDriver{})

	rows, err := db.Query("DELETE FROM accounts") // flagged: delete-without-where
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := rep.count(); got != 1 {
		t.Errorf("a query the base declined with ErrSkip must be analyzed once, got %d", got)
	}
}

func TestDriver_ErrSkipExecAnalyzedOnce(t *testing.T) {
	db, rep := openFakeGuarded(t, fakeErrSkipDriver{})

	if _, err := db.Exec("DELETE FROM accounts"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if got := rep.count(); got != 1 {
		t.Errorf("an exec the base declined with ErrSkip must be analyzed once, got %d", got)
	}
}

func TestDriver_ErrSkipN1CountedOnce(t *testing.T) {
	assertOneQueryDoesNotTripN1(t, fakeErrSkipDriver{})
}

// A base that does handle the direct path is unaffected: it executes the
// query itself, database/sql never falls back, and the single analysis
// happens at the conn level.
func TestDriver_DirectQueryerAnalyzedOnce(t *testing.T) {
	db, rep := openFakeGuarded(t, fakeQueryerDriver{})

	rows, err := db.Query("DELETE FROM accounts") // flagged: delete-without-where
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := rep.count(); got != 1 {
		t.Errorf("a query the base executed directly must be analyzed once, got %d", got)
	}

	if _, err := db.Exec("DELETE FROM accounts"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if got := rep.count(); got != 2 {
		t.Errorf("the direct exec path must add exactly one analysis, got %d total", got)
	}
}

func TestDriver_DirectQueryerN1CountedOnce(t *testing.T) {
	assertOneQueryDoesNotTripN1(t, fakeQueryerDriver{})
}

// database/sql retries a query on driver.ErrBadConn (twice on a pooled
// connection, then once on a brand-new one). Each retry re-enters the wrapper,
// so analyzing a declined attempt multiplies one logical query by up to three
// — the same N+1 inflation as #67, from the other per-call "did not run"
// answer.
func TestDriver_BadConnRetryAnalyzedOnce(t *testing.T) {
	base := &fakeBadConnDriver{}
	base.fails.Store(2)
	db, rep := openFakeGuarded(t, base)

	rows, err := db.Query("DELETE FROM accounts") // flagged: delete-without-where
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := base.fails.Load(); got != -1 {
		t.Fatalf("expected the base to be called three times (two declines, one success), got %d remaining", got)
	}
	if got := rep.count(); got != 1 {
		t.Errorf("a query retried past ErrBadConn must be analyzed once, got %d", got)
	}
}

func TestDriver_BadConnRetryN1CountedOnce(t *testing.T) {
	base := &fakeBadConnDriver{}
	base.fails.Store(2)
	db, rep := openFakeGuarded(t, base, WithN1Detection(2, time.Minute))

	rows, err := db.Query("SELECT id, name FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := rep.count(); got != 0 {
		t.Errorf("one logical query must not trip N+1 (threshold 2) however often it was retried; got %d reports", got)
	}
}

// A statement whose arguments the legacy conversion rejects never reaches the
// base, so there is nothing to analyze: fakeStmt implements neither
// StmtQueryContext nor NamedValueChecker, and named parameters have no
// positional form.
func TestDriver_RejectedNamedArgsNotAnalyzed(t *testing.T) {
	db, rep := openFakeGuarded(t, fakeNoQueryerDriver{})

	// select-star would be reported if this were analyzed.
	rows, err := db.Query("SELECT * FROM users WHERE id = :id", sql.Named("id", 1))
	if err == nil {
		rows.Close()
		t.Fatal("expected the named-parameter conversion to fail")
	}
	if !strings.Contains(err.Error(), "does not support named parameters") {
		t.Fatalf("expected the wrapper's own conversion error, got %v", err)
	}

	if got := rep.count(); got != 0 {
		t.Errorf("a query rejected before it reached the base must not be analyzed, got %d", got)
	}
}

// assertOneQueryDoesNotTripN1 runs a single non-flagged query against base
// with the N+1 threshold at 2: nothing may be reported, because one execution
// must move the counter by one.
func assertOneQueryDoesNotTripN1(t *testing.T, base driver.Driver) {
	t.Helper()
	db, rep := openFakeGuarded(t, base, WithN1Detection(2, time.Minute))

	rows, err := db.Query("SELECT id, name FROM users WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if got := rep.count(); got != 0 {
		t.Errorf("one logical query must not trip N+1 (threshold 2); got %d reports", got)
	}
}
