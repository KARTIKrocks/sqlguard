package xormguard

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
	_ "github.com/mattn/go-sqlite3"
	"xorm.io/xorm"
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

// newEngineWithCapture spins up an in-memory sqlite-backed *xorm.Engine with
// the sqlguard hook registered, so the integration runs end-to-end
// (contexts.Hook seam → driver round trip) rather than mocked. The hook is
// added after seeding so the capture starts clean.
func newEngineWithCapture(t *testing.T, opts ...middleware.Option) (*xorm.Engine, *capture, *Hook) {
	t.Helper()
	engine, err := xorm.NewEngine("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	if _, err := engine.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := engine.Exec("INSERT INTO users (id, email) VALUES (?, ?)", 1, "leak@example.com"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cap := &capture{}
	opts = append([]middleware.Option{middleware.WithReporter(cap)}, opts...)
	hook := New(opts...)
	engine.AddHook(hook)
	return engine, cap, hook
}

func TestHook_DetectsRawSelectStar(t *testing.T) {
	engine, cap, _ := newEngineWithCapture(t)
	if _, err := engine.QueryString("SELECT * FROM users"); err != nil {
		t.Fatalf("QueryString: %v", err)
	}
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding, got %+v", cap.snapshot())
	}
}

// TestHook_RedactsLiteralsByDefault asserts the headline redaction guarantee:
// single-quoted literals never reach Result.Query and Fingerprint is always
// populated.
func TestHook_RedactsLiteralsByDefault(t *testing.T) {
	engine, cap, _ := newEngineWithCapture(t)
	if _, err := engine.QueryString("SELECT * FROM users WHERE email = 'leak@example.com'"); err != nil {
		t.Fatalf("QueryString: %v", err)
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
	engine, cap, _ := newEngineWithCapture(t, middleware.WithSlowQueryThreshold(0))
	if _, err := engine.QueryString("SELECT id FROM users WHERE id = 1"); err != nil {
		t.Fatalf("QueryString: %v", err)
	}
	if !cap.has("slow-query") {
		t.Fatalf("expected slow-query finding with zero threshold, got %+v", cap.snapshot())
	}
}

func TestHook_SlowQuerySuppressedOnError(t *testing.T) {
	engine, cap, _ := newEngineWithCapture(t, middleware.WithSlowQueryThreshold(0))
	_, err := engine.QueryString("SELECT id FROM no_such_table_xyz WHERE id = 1")
	if err == nil {
		t.Fatal("expected error from selecting a missing table")
	}
	if cap.has("slow-query") {
		t.Fatalf("slow-query must not fire when the query failed; got %+v", cap.snapshot())
	}
}

func TestHook_NPlusOneAcrossCalls(t *testing.T) {
	engine, cap, _ := newEngineWithCapture(t, middleware.WithN1Detection(3, time.Second))
	for range 3 {
		if _, err := engine.QueryString("SELECT id FROM users WHERE id = 1"); err != nil {
			t.Fatalf("QueryString: %v", err)
		}
	}
	if !cap.has("n-plus-one") {
		t.Fatalf("expected n-plus-one finding after 3 identical queries, got %+v", cap.snapshot())
	}
}

func TestHook_ResetN1ClearsState(t *testing.T) {
	engine, cap, hook := newEngineWithCapture(t, middleware.WithN1Detection(3, time.Second))
	for range 2 {
		if _, err := engine.QueryString("SELECT id FROM users WHERE id = 1"); err != nil {
			t.Fatalf("QueryString: %v", err)
		}
	}
	hook.ResetN1()
	if _, err := engine.QueryString("SELECT id FROM users WHERE id = 1"); err != nil {
		t.Fatalf("QueryString: %v", err)
	}
	if cap.has("n-plus-one") {
		t.Fatalf("n-plus-one should not fire — ResetN1 zeroed the counter; got %+v", cap.snapshot())
	}
}

// Proves UPDATE / DELETE statements also flow through Guard.
func TestHook_UpdateAndDeleteAnalyzed(t *testing.T) {
	engine, cap, _ := newEngineWithCapture(t)
	if _, err := engine.Exec("UPDATE users SET email = 'x'"); err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if !cap.has("update-without-where") {
		t.Fatalf("expected update-without-where, got %+v", cap.snapshot())
	}
	if _, err := engine.Exec("DELETE FROM users"); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if !cap.has("delete-without-where") {
		t.Fatalf("expected delete-without-where, got %+v", cap.snapshot())
	}
}
