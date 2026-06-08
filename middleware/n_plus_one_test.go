package middleware

import (
	"fmt"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"numbers", "SELECT * FROM users WHERE id = 42", "SELECT * FROM users WHERE id = ?"},
		{"strings", "SELECT * FROM users WHERE name = 'alice'", "SELECT * FROM users WHERE name = ?"},
		{"mixed", "SELECT * FROM users WHERE id = 1 AND name = 'bob'", "SELECT * FROM users WHERE id = ? AND name = ?"},
		{"no literals", "SELECT * FROM users WHERE id = ?", "SELECT * FROM users WHERE id = ?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeQuery(tt.input)
			if got != tt.want {
				t.Errorf("normalizeQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestQueryTracker_DetectsN1(t *testing.T) {
	var reported []analyzer.Result
	tracker := NewQueryTracker(3, 5*time.Second, func(results []analyzer.Result) {
		reported = append(reported, results...)
	})

	// Same pattern 3 times should trigger
	tracker.Track("SELECT * FROM orders WHERE user_id = 1")
	tracker.Track("SELECT * FROM orders WHERE user_id = 2")
	tracker.Track("SELECT * FROM orders WHERE user_id = 3")

	if len(reported) != 1 {
		t.Fatalf("expected 1 N+1 report, got %d", len(reported))
	}
	if reported[0].RuleName != "n-plus-one" {
		t.Errorf("expected rule n-plus-one, got %s", reported[0].RuleName)
	}
}

func TestQueryTracker_DifferentPatterns(t *testing.T) {
	var reported []analyzer.Result
	tracker := NewQueryTracker(3, 5*time.Second, func(results []analyzer.Result) {
		reported = append(reported, results...)
	})

	// Different patterns should not trigger
	tracker.Track("SELECT * FROM orders WHERE user_id = 1")
	tracker.Track("SELECT * FROM users WHERE id = 1")
	tracker.Track("SELECT * FROM products WHERE id = 1")

	if len(reported) != 0 {
		t.Errorf("expected no reports for different patterns, got %d", len(reported))
	}
}

func TestQueryTracker_BelowThreshold(t *testing.T) {
	var reported []analyzer.Result
	tracker := NewQueryTracker(5, 5*time.Second, func(results []analyzer.Result) {
		reported = append(reported, results...)
	})

	// Only 3 of same pattern, threshold is 5
	tracker.Track("SELECT * FROM orders WHERE user_id = 1")
	tracker.Track("SELECT * FROM orders WHERE user_id = 2")
	tracker.Track("SELECT * FROM orders WHERE user_id = 3")

	if len(reported) != 0 {
		t.Errorf("expected no reports below threshold, got %d", len(reported))
	}
}

func TestQueryTracker_ReportsOnlyOnce(t *testing.T) {
	var reported []analyzer.Result
	tracker := NewQueryTracker(2, 5*time.Second, func(results []analyzer.Result) {
		reported = append(reported, results...)
	})

	tracker.Track("SELECT * FROM orders WHERE user_id = 1")
	tracker.Track("SELECT * FROM orders WHERE user_id = 2")
	tracker.Track("SELECT * FROM orders WHERE user_id = 3")
	tracker.Track("SELECT * FROM orders WHERE user_id = 4")

	if len(reported) != 1 {
		t.Errorf("expected exactly 1 report (not per-query), got %d", len(reported))
	}
}

func TestQueryTracker_Reset(t *testing.T) {
	var reported []analyzer.Result
	tracker := NewQueryTracker(2, 5*time.Second, func(results []analyzer.Result) {
		reported = append(reported, results...)
	})

	tracker.Track("SELECT * FROM orders WHERE user_id = 1")
	tracker.Reset()
	tracker.Track("SELECT * FROM orders WHERE user_id = 2")

	// After reset, count should restart
	if len(reported) != 0 {
		t.Errorf("expected no reports after reset, got %d", len(reported))
	}
}

// N+1 detection through the driver path is covered by
// TestDriver_N1Detection in driver_test.go. QueryTracker.Reset is
// exercised directly above; per-request reset is no longer exposed on
// the *sql.DB returned by the driver wrapper.

func TestQueryTracker_BoundedAtMaxKeys(t *testing.T) {
	qt := &QueryTracker{
		queries:   make(map[string]*queryRecord),
		threshold: 1000,      // high so nothing reports
		window:    time.Hour, // long so nothing expires (eviction frees nothing)
		maxKeys:   3,
		reporter:  func([]analyzer.Result) {},
	}

	// Distinct query *shapes* (distinct column names → distinct fingerprints),
	// far more than maxKeys, all in-window. The map must stay capped.
	for i := range 50 {
		qt.Track(fmt.Sprintf("SELECT col%d FROM t WHERE id = ?", i))
	}

	if len(qt.queries) > qt.maxKeys {
		t.Errorf("tracker map grew past maxKeys: %d > %d", len(qt.queries), qt.maxKeys)
	}
}

func TestQueryTracker_TrackedKeyHonoredAtCap(t *testing.T) {
	var reports int
	qt := &QueryTracker{
		queries:   make(map[string]*queryRecord),
		threshold: 3,
		window:    time.Hour,
		maxKeys:   2,
		reporter:  func([]analyzer.Result) { reports++ },
	}

	// "cola" gets tracked to count 2 (below threshold), then the cap fills.
	qt.Track("SELECT cola FROM t")
	qt.Track("SELECT cola FROM t")
	qt.Track("SELECT colb FROM t") // map now {cola, colb}, at cap

	// A brand-new key at the cap is dropped (map stays bounded)...
	qt.Track("SELECT colc FROM t")
	if len(qt.queries) > qt.maxKeys {
		t.Fatalf("map exceeded cap: %d", len(qt.queries))
	}

	// ...but the already-tracked "cola" still increments to threshold and fires
	// exactly once — in-flight detection is never lost to the cap.
	qt.Track("SELECT cola FROM t")
	if reports != 1 {
		t.Errorf("expected the tracked key to still reach threshold and report once, got %d", reports)
	}
}
