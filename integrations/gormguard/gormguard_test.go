package gormguard

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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
	ID    int64 `gorm:"primaryKey"`
	Email string
}

// newDBWithCapture spins up an in-memory sqlite-backed *gorm.DB with the
// sqlguard plugin registered, so the integration runs end-to-end (callback
// seam → driver round trip) rather than mocked.
func newDBWithCapture(t *testing.T, opts ...middleware.Option) (*gorm.DB, *capture, *Plugin) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&user{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Create(&user{ID: 1, Email: "leak@example.com"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	cap := &capture{}
	opts = append([]middleware.Option{middleware.WithReporter(cap)}, opts...)
	plugin := New(opts...)
	if err := db.Use(plugin); err != nil {
		t.Fatalf("db.Use: %v", err)
	}
	// Reset capture so the seed INSERT's findings don't pollute test
	// assertions — every test wants only the findings from its own queries.
	cap.mu.Lock()
	cap.r = nil
	cap.mu.Unlock()
	return db, cap, plugin
}

func TestPlugin_DetectsRawSelectStar(t *testing.T) {
	db, cap, _ := newDBWithCapture(t)
	var us []user
	if err := db.Raw("SELECT * FROM users").Scan(&us).Error; err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding, got %+v", cap.snapshot())
	}
}

// TestPlugin_RedactsLiteralsByDefault is the headline 11.1 regression:
// the old hand-rolled after() set Result.Query to the raw SQL, so single-
// quoted literals leaked into log sinks. After the Guard rewrite Query
// must be the redacted form and Fingerprint must always be populated.
func TestPlugin_RedactsLiteralsByDefault(t *testing.T) {
	db, cap, _ := newDBWithCapture(t)
	var us []user
	if err := db.Raw("SELECT * FROM users WHERE email = 'leak@example.com'").Scan(&us).Error; err != nil {
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

// TestPlugin_SlowQueryReportedOnSuccess uses a zero threshold so any
// successful query trips the slow-query path. Threshold arithmetic is
// covered by middleware.Guard's own tests — here we only assert that the
// integration's after-callback drives CheckLatency on success.
func TestPlugin_SlowQueryReportedOnSuccess(t *testing.T) {
	db, cap, _ := newDBWithCapture(t, middleware.WithSlowQueryThreshold(0))
	var u user
	if err := db.First(&u, 1).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if !cap.has("slow-query") {
		t.Fatalf("expected slow-query finding with zero threshold, got %+v", cap.snapshot())
	}
}

func TestPlugin_SlowQuerySuppressedOnError(t *testing.T) {
	db, cap, _ := newDBWithCapture(t, middleware.WithSlowQueryThreshold(0))
	// Force a SQL error: SELECT from a missing table via Raw so we hit the
	// after-callback with db.Error != nil.
	var dst int
	err := db.Raw("SELECT id FROM no_such_table_xyz WHERE id = 1").Scan(&dst).Error
	if err == nil {
		t.Fatal("expected error from selecting a missing table")
	}
	if cap.has("slow-query") {
		t.Fatalf("slow-query must not fire when the query failed; got %+v", cap.snapshot())
	}
}

func TestPlugin_NPlusOneAcrossCalls(t *testing.T) {
	db, cap, _ := newDBWithCapture(t, middleware.WithN1Detection(3, time.Second))
	var u user
	for range 3 {
		if err := db.Raw("SELECT id FROM users WHERE id = 1").Scan(&u).Error; err != nil {
			t.Fatalf("Raw: %v", err)
		}
	}
	if !cap.has("n-plus-one") {
		t.Fatalf("expected n-plus-one finding after 3 identical queries, got %+v", cap.snapshot())
	}
}

func TestPlugin_ResetN1ClearsState(t *testing.T) {
	db, cap, plugin := newDBWithCapture(t, middleware.WithN1Detection(3, time.Second))
	var u user
	for range 2 {
		if err := db.Raw("SELECT id FROM users WHERE id = 1").Scan(&u).Error; err != nil {
			t.Fatalf("Raw: %v", err)
		}
	}
	plugin.ResetN1()
	if err := db.Raw("SELECT id FROM users WHERE id = 1").Scan(&u).Error; err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if cap.has("n-plus-one") {
		t.Fatalf("n-plus-one should not fire — ResetN1 zeroed the counter; got %+v", cap.snapshot())
	}
}

// Proves the UPDATE / DELETE callbacks also flow through Guard.
func TestPlugin_UpdateAndDeleteCallbacksAnalyzed(t *testing.T) {
	db, cap, _ := newDBWithCapture(t)
	if err := db.WithContext(context.Background()).Exec("UPDATE users SET email = 'x'").Error; err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if !cap.has("update-without-where") {
		t.Fatalf("expected update-without-where from update callback, got %+v", cap.snapshot())
	}

	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if !cap.has("delete-without-where") {
		t.Fatalf("expected delete-without-where from delete callback, got %+v", cap.snapshot())
	}
}

func TestRegister_ReturnsNoError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := Register(db); err != nil {
		t.Fatalf("Register: %v", err)
	}
}
