package middleware

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// countingReporter records every Result it is handed, concurrency-safe.
type countingReporter struct {
	mu      sync.Mutex
	results []analyzer.Result
}

func (c *countingReporter) Report(rs []analyzer.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, rs...)
}

func (c *countingReporter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.results)
}

func TestDeduper_AllowsFirstSuppressesRepeatThenReReportsAfterWindow(t *testing.T) {
	d := newDeduper(time.Minute)
	now := time.Now()

	if !d.allow("fp", "select-star", now) {
		t.Fatal("first occurrence should be allowed")
	}
	if d.allow("fp", "select-star", now) {
		t.Error("repeat within window should be suppressed")
	}
	if !d.allow("fp", "select-star", now.Add(2*time.Minute)) {
		t.Error("occurrence after window elapsed should be allowed again")
	}
}

func TestDeduper_DistinctIdentitiesIndependent(t *testing.T) {
	d := newDeduper(time.Minute)
	now := time.Now()

	// Different rule, same fingerprint.
	if !d.allow("fp", "select-star", now) || !d.allow("fp", "select-without-limit", now) {
		t.Error("distinct rules on the same fingerprint should each be allowed once")
	}
	// Different fingerprint, same rule.
	if !d.allow("fp2", "select-star", now) {
		t.Error("same rule on a distinct fingerprint should be allowed")
	}
}

func TestDeduper_DisabledWindowAlwaysAllows(t *testing.T) {
	d := newDeduper(0)
	now := time.Now()
	for i := range 5 {
		if !d.allow("fp", "select-star", now) {
			t.Errorf("window<=0 disables dedup; call %d should be allowed", i)
		}
	}
}

func TestDeduper_BoundedAtMaxKeys(t *testing.T) {
	// All entries stay in-window, so eviction frees nothing: new keys past the
	// cap must be dropped rather than grow the map without bound.
	d := &deduper{seen: map[string]time.Time{}, window: time.Hour, maxKeys: 2}
	now := time.Now()

	if !d.allow("a", "r", now) || !d.allow("b", "r", now) {
		t.Fatal("first two distinct keys should be allowed")
	}
	if d.allow("c", "r", now) {
		t.Error("a new key past maxKeys with nothing to evict should be dropped")
	}
	if len(d.seen) > d.maxKeys {
		t.Errorf("map grew past maxKeys: %d", len(d.seen))
	}
	// An already-tracked key is still served (dedup state is not lost).
	if d.allow("a", "r", now) {
		t.Error("an in-window tracked key should remain suppressed, not re-reported")
	}
}

func TestGuard_DedupSuppressesRepeatStaticFindings(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithReporter(rep)) // default dedup window = 1m

	// DELETE without WHERE triggers exactly one rule (delete-without-where).
	for range 10 {
		g.Check("DELETE FROM accounts")
	}

	if got := rep.count(); got != 1 {
		t.Errorf("expected 1 static finding for a repeated query, got %d", got)
	}
}

func TestGuard_DedupDisabledReportsEveryTime(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithReporter(rep), WithFindingDedup(0))

	for range 5 {
		g.Check("DELETE FROM accounts")
	}

	if got := rep.count(); got != 5 {
		t.Errorf("with dedup disabled expected 5 findings, got %d", got)
	}
}

func TestGuard_DedupIsPerIdentityNotPerQuery(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithReporter(rep))

	// Two literal variants share one fingerprint -> one select-star finding.
	g.Check("SELECT * FROM users WHERE id = 1")
	g.Check("SELECT * FROM users WHERE id = 2")
	// A genuinely different flagged query is reported independently.
	g.Check("DELETE FROM accounts")

	if got := rep.count(); got != 2 {
		t.Errorf("expected 2 findings (one per identity), got %d", got)
	}
}

func TestGuard_DedupConcurrent(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithReporter(rep))

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			g.Check("DELETE FROM accounts")
		})
	}
	wg.Wait()

	if got := rep.count(); got != 1 {
		t.Errorf("expected exactly 1 finding under concurrency, got %d", got)
	}
}

func TestDriver_DedupRepeatedStaticFinding(t *testing.T) {
	db, buf := guardedWithBuffer(t)

	for range 10 {
		rows, err := db.Query("SELECT * FROM users")
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		rows.Close()
	}

	if n := strings.Count(buf.String(), "select-star"); n != 1 {
		t.Errorf("expected select-star reported once across 10 executions, got %d", n)
	}
}
