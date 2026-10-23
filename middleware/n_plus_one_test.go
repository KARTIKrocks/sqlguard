package middleware

import (
	"fmt"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestQueryTracker_DetectsN1(t *testing.T) {
	var reported []analyzer.Result
	tracker := NewQueryTracker(nil, 3, 5*time.Second, analyzer.SeverityWarning, func(results []analyzer.Result) {
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
	tracker := NewQueryTracker(nil, 3, 5*time.Second, analyzer.SeverityWarning, func(results []analyzer.Result) {
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
	tracker := NewQueryTracker(nil, 5, 5*time.Second, analyzer.SeverityWarning, func(results []analyzer.Result) {
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
	tracker := NewQueryTracker(nil, 2, 5*time.Second, analyzer.SeverityWarning, func(results []analyzer.Result) {
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
	tracker := NewQueryTracker(nil, 2, 5*time.Second, analyzer.SeverityWarning, func(results []analyzer.Result) {
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
	// High threshold so nothing reports, long window so nothing expires.
	qt := NewQueryTracker(nil, 1000, time.Hour, analyzer.SeverityWarning, func([]analyzer.Result) {})
	qt.maxKeys = 3

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
	qt := NewQueryTracker(nil, 3, time.Hour, analyzer.SeverityWarning, func([]analyzer.Result) { reports++ })
	qt.maxKeys = 2

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
