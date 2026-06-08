package entguard

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
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

// newDriverWithCapture opens a real sqlite-backed ent dialect.Driver, seeds it
// through the *unwrapped* driver (so the capture starts clean), then wraps it
// with the sqlguard decorator. The integration thus runs end-to-end
// (dialect.Driver seam → database/sql round trip) rather than mocked.
func newDriverWithCapture(t *testing.T, opts ...middleware.Option) (*Driver, *capture) {
	t.Helper()
	drv, err := entsql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("entsql.Open: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })

	ctx := context.Background()
	if err := drv.Exec(ctx, "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)", []any{}, nil); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := drv.Exec(ctx, "INSERT INTO users (id, email) VALUES (?, ?)", []any{1, "leak@example.com"}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cap := &capture{}
	opts = append([]middleware.Option{middleware.WithReporter(cap)}, opts...)
	return Wrap(drv, opts...), cap
}

func query(t *testing.T, ctx context.Context, q interface {
	Query(context.Context, string, any, any) error
}, sqlText string) error {
	t.Helper()
	var rows entsql.Rows
	err := q.Query(ctx, sqlText, []any{}, &rows)
	if err == nil {
		_ = rows.Close()
	}
	return err
}

func TestDriver_DetectsSelectStar(t *testing.T) {
	drv, cap := newDriverWithCapture(t)
	if err := query(t, context.Background(), drv, "SELECT * FROM users"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding, got %+v", cap.snapshot())
	}
}

// TestDriver_RedactsLiteralsByDefault asserts the headline redaction
// guarantee: single-quoted literals never reach Result.Query and Fingerprint
// is always populated.
func TestDriver_RedactsLiteralsByDefault(t *testing.T) {
	drv, cap := newDriverWithCapture(t)
	if err := query(t, context.Background(), drv, "SELECT * FROM users WHERE email = 'leak@example.com'"); err != nil {
		t.Fatalf("Query: %v", err)
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

func TestDriver_SlowQueryReportedOnSuccess(t *testing.T) {
	drv, cap := newDriverWithCapture(t, middleware.WithSlowQueryThreshold(0))
	if err := query(t, context.Background(), drv, "SELECT id FROM users WHERE id = 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !cap.has("slow-query") {
		t.Fatalf("expected slow-query finding with zero threshold, got %+v", cap.snapshot())
	}
}

func TestDriver_SlowQuerySuppressedOnError(t *testing.T) {
	drv, cap := newDriverWithCapture(t, middleware.WithSlowQueryThreshold(0))
	err := query(t, context.Background(), drv, "SELECT id FROM no_such_table_xyz WHERE id = 1")
	if err == nil {
		t.Fatal("expected error from selecting a missing table")
	}
	if cap.has("slow-query") {
		t.Fatalf("slow-query must not fire when the query failed; got %+v", cap.snapshot())
	}
}

func TestDriver_NPlusOneAcrossCalls(t *testing.T) {
	drv, cap := newDriverWithCapture(t, middleware.WithN1Detection(3, time.Second))
	for range 3 {
		if err := query(t, context.Background(), drv, "SELECT id FROM users WHERE id = 1"); err != nil {
			t.Fatalf("Query: %v", err)
		}
	}
	if !cap.has("n-plus-one") {
		t.Fatalf("expected n-plus-one finding after 3 identical queries, got %+v", cap.snapshot())
	}
}

func TestDriver_ResetN1ClearsState(t *testing.T) {
	drv, cap := newDriverWithCapture(t, middleware.WithN1Detection(3, time.Second))
	for range 2 {
		if err := query(t, context.Background(), drv, "SELECT id FROM users WHERE id = 1"); err != nil {
			t.Fatalf("Query: %v", err)
		}
	}
	drv.ResetN1()
	if err := query(t, context.Background(), drv, "SELECT id FROM users WHERE id = 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if cap.has("n-plus-one") {
		t.Fatalf("n-plus-one should not fire — ResetN1 zeroed the counter; got %+v", cap.snapshot())
	}
}

func TestDriver_ExecUpdateAnalyzed(t *testing.T) {
	drv, cap := newDriverWithCapture(t)
	if err := drv.Exec(context.Background(), "UPDATE users SET email = 'x'", []any{}, nil); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !cap.has("update-without-where") {
		t.Fatalf("expected update-without-where, got %+v", cap.snapshot())
	}
}

// TestDriver_TxQueriesAnalyzed proves the transaction wrapper also routes
// in-tx statements through Guard — a query class the database/sql-only path
// would miss if Tx weren't decorated.
func TestDriver_TxQueriesAnalyzed(t *testing.T) {
	drv, cap := newDriverWithCapture(t)
	ctx := context.Background()
	tx, err := drv.Tx(ctx)
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if err := query(t, ctx, tx, "SELECT * FROM users"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("tx Query: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding from in-tx query, got %+v", cap.snapshot())
	}
}
