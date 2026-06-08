package sqlxguard

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// capture is a thread-safe in-memory Reporter for assertions.
type capture struct {
	mu sync.Mutex
	r  []analyzer.Result
}

func (c *capture) Report(rs []analyzer.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.r = append(c.r, rs...)
}

func (c *capture) snapshot() []analyzer.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]analyzer.Result, len(c.r))
	copy(out, c.r)
	return out
}

func (c *capture) has(rule string) bool {
	for _, r := range c.snapshot() {
		if r.RuleName == rule {
			return true
		}
	}
	return false
}

// newWrappedWithCapture spins up an in-memory sqlite-backed *sqlx.DB so the
// integration is exercised end-to-end (sqlx extension method → database/sql →
// real driver round trip) rather than mocked.
func newWrappedWithCapture(t *testing.T, opts ...middleware.Option) (*WrappedDB, *capture) {
	t.Helper()
	sqlxDB, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sqlx.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlxDB.Close() })
	if _, err := sqlxDB.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := sqlxDB.Exec(`INSERT INTO users (id, email) VALUES (1, 'leak@example.com')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cap := &capture{}
	opts = append([]middleware.Option{middleware.WithReporter(cap)}, opts...)
	return WrapSqlx(sqlxDB, opts...), cap
}

type user struct {
	ID    int64  `db:"id"`
	Email string `db:"email"`
}

func TestWrappedDB_DetectsSelectStar(t *testing.T) {
	w, cap := newWrappedWithCapture(t)
	var us []user
	if err := w.Select(&us, "SELECT * FROM users"); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding, got %+v", cap.snapshot())
	}
}

// TestWrappedDB_RedactsLiteralsByDefault is the headline 11.1 regression:
// the old hand-rolled check() set Result.Query to the raw SQL, so single-
// quoted literals leaked into log sinks. After the Guard rewrite Query
// must be the redacted form and Fingerprint must always be populated.
func TestWrappedDB_RedactsLiteralsByDefault(t *testing.T) {
	w, cap := newWrappedWithCapture(t)
	var us []user
	if err := w.Select(&us, "SELECT * FROM users WHERE email = 'leak@example.com'"); err != nil {
		t.Fatalf("Select: %v", err)
	}
	results := cap.snapshot()
	if len(results) == 0 {
		t.Fatal("expected at least one finding")
	}
	for _, r := range results {
		if strings.Contains(r.Query, "leak@example.com") {
			t.Errorf("literal leaked into Result.Query: %q (rule=%s)", r.Query, r.RuleName)
		}
		if r.Fingerprint == "" {
			t.Errorf("Fingerprint must always be populated, got empty for rule %s", r.RuleName)
		}
	}
}

// TestWrappedDB_SlowQueryReportedOnSuccess uses a zero threshold so any
// successful round trip trips the slow-query path. The integration-level
// claim under test is "slow-query check runs on success", not the threshold
// arithmetic itself (that lives in middleware.Guard's own tests).
func TestWrappedDB_SlowQueryReportedOnSuccess(t *testing.T) {
	w, cap := newWrappedWithCapture(t, middleware.WithSlowQueryThreshold(0))
	var u user
	if err := w.Get(&u, "SELECT id FROM users WHERE id = 1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !cap.has("slow-query") {
		t.Fatalf("expected slow-query finding with zero threshold, got %+v", cap.snapshot())
	}
}

func TestWrappedDB_SlowQuerySuppressedOnError(t *testing.T) {
	w, cap := newWrappedWithCapture(t, middleware.WithSlowQueryThreshold(0))
	var u user
	if err := w.Get(&u, "SELECT id FROM no_such_table_xyz WHERE id = 1"); err == nil {
		t.Fatal("expected error from selecting a missing table")
	}
	if cap.has("slow-query") {
		t.Fatalf("slow-query must not fire when the query failed; got %+v", cap.snapshot())
	}
}

func TestWrappedDB_NPlusOneAcrossCalls(t *testing.T) {
	w, cap := newWrappedWithCapture(t, middleware.WithN1Detection(3, time.Second))
	var u user
	for range 3 {
		if err := w.Get(&u, "SELECT id FROM users WHERE id = 1"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if !cap.has("n-plus-one") {
		t.Fatalf("expected n-plus-one finding after 3 identical queries, got %+v", cap.snapshot())
	}
}

func TestWrappedDB_ResetN1ClearsState(t *testing.T) {
	w, cap := newWrappedWithCapture(t, middleware.WithN1Detection(3, time.Second))
	var u user
	for range 2 {
		if err := w.Get(&u, "SELECT id FROM users WHERE id = 1"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	w.ResetN1()
	if err := w.Get(&u, "SELECT id FROM users WHERE id = 1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cap.has("n-plus-one") {
		t.Fatalf("n-plus-one should not fire — ResetN1 zeroed the counter; got %+v", cap.snapshot())
	}
}

// Proves the non-SELECT and *Context paths also flow through Guard.
func TestWrappedDB_ExecAndContextVariantsAnalyzed(t *testing.T) {
	w, cap := newWrappedWithCapture(t)
	if _, err := w.Exec("DELETE FROM users"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !cap.has("delete-without-where") {
		t.Fatalf("expected delete-without-where from Exec path, got %+v", cap.snapshot())
	}

	if _, err := w.ExecContext(context.Background(), "UPDATE users SET email = 'x'"); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	if !cap.has("update-without-where") {
		t.Fatalf("expected update-without-where from ExecContext path, got %+v", cap.snapshot())
	}
}

func TestWrapSqlx_NilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil *sqlx.DB")
		}
	}()
	WrapSqlx(nil)
}
