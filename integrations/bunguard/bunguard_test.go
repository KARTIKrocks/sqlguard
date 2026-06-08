package bunguard

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
	_ "github.com/mattn/go-sqlite3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
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

type user struct {
	bun.BaseModel `bun:"table:users"`
	ID            int64  `bun:"id,pk"`
	Email         string `bun:"email"`
}

// newDBWithCapture spins up an in-memory sqlite-backed *bun.DB with the
// sqlguard hook registered, so the integration runs end-to-end (QueryHook
// seam → driver round trip) rather than mocked.
func newDBWithCapture(t *testing.T, opts ...middleware.Option) (*bun.DB, *capture, *QueryHook) {
	t.Helper()
	sqldb, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	db := bun.NewDB(sqldb, sqlitedialect.New())

	ctx := context.Background()
	if _, err := db.NewCreateTable().Model((*user)(nil)).Exec(ctx); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.NewInsert().Model(&user{ID: 1, Email: "leak@example.com"}).Exec(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cap := &capture{}
	opts = append([]middleware.Option{middleware.WithReporter(cap)}, opts...)
	hook := New(opts...)
	db.AddQueryHook(hook)
	// Hook registered after seeding, so capture starts clean — every test
	// asserts only on findings from its own queries.
	return db, cap, hook
}

func TestHook_DetectsRawSelectStar(t *testing.T) {
	db, cap, _ := newDBWithCapture(t)
	var us []user
	if err := db.NewRaw("SELECT * FROM users").Scan(context.Background(), &us); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding, got %+v", cap.snapshot())
	}
}

// TestHook_RedactsLiteralsByDefault asserts the headline redaction guarantee:
// single-quoted literals never reach Result.Query and Fingerprint is always
// populated.
func TestHook_RedactsLiteralsByDefault(t *testing.T) {
	db, cap, _ := newDBWithCapture(t)
	var us []user
	if err := db.NewRaw("SELECT * FROM users WHERE email = 'leak@example.com'").Scan(context.Background(), &us); err != nil {
		t.Fatalf("Raw: %v", err)
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

func TestHook_SlowQueryReportedOnSuccess(t *testing.T) {
	db, cap, _ := newDBWithCapture(t, middleware.WithSlowQueryThreshold(0))
	var u user
	if err := db.NewSelect().Model(&u).Where("id = ?", 1).Scan(context.Background()); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !cap.has("slow-query") {
		t.Fatalf("expected slow-query finding with zero threshold, got %+v", cap.snapshot())
	}
}

func TestHook_SlowQuerySuppressedOnError(t *testing.T) {
	db, cap, _ := newDBWithCapture(t, middleware.WithSlowQueryThreshold(0))
	var dst int
	err := db.NewRaw("SELECT id FROM no_such_table_xyz WHERE id = 1").Scan(context.Background(), &dst)
	if err == nil {
		t.Fatal("expected error from selecting a missing table")
	}
	if cap.has("slow-query") {
		t.Fatalf("slow-query must not fire when the query failed; got %+v", cap.snapshot())
	}
}

func TestHook_NPlusOneAcrossCalls(t *testing.T) {
	db, cap, _ := newDBWithCapture(t, middleware.WithN1Detection(3, time.Second))
	var u user
	for range 3 {
		if err := db.NewRaw("SELECT id FROM users WHERE id = 1").Scan(context.Background(), &u); err != nil {
			t.Fatalf("Raw: %v", err)
		}
	}
	if !cap.has("n-plus-one") {
		t.Fatalf("expected n-plus-one finding after 3 identical queries, got %+v", cap.snapshot())
	}
}

func TestHook_ResetN1ClearsState(t *testing.T) {
	db, cap, hook := newDBWithCapture(t, middleware.WithN1Detection(3, time.Second))
	var u user
	for range 2 {
		if err := db.NewRaw("SELECT id FROM users WHERE id = 1").Scan(context.Background(), &u); err != nil {
			t.Fatalf("Raw: %v", err)
		}
	}
	hook.ResetN1()
	if err := db.NewRaw("SELECT id FROM users WHERE id = 1").Scan(context.Background(), &u); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if cap.has("n-plus-one") {
		t.Fatalf("n-plus-one should not fire — ResetN1 zeroed the counter; got %+v", cap.snapshot())
	}
}

// Proves UPDATE / DELETE statements also flow through Guard.
func TestHook_UpdateAndDeleteAnalyzed(t *testing.T) {
	db, cap, _ := newDBWithCapture(t)
	ctx := context.Background()
	if _, err := db.NewRaw("UPDATE users SET email = 'x'").Exec(ctx); err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if !cap.has("update-without-where") {
		t.Fatalf("expected update-without-where, got %+v", cap.snapshot())
	}
	if _, err := db.NewRaw("DELETE FROM users").Exec(ctx); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if !cap.has("delete-without-where") {
		t.Fatalf("expected delete-without-where, got %+v", cap.snapshot())
	}
}
