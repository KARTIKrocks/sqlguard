package middleware

import (
	"fmt"
	"sync"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// normalizeQuery is the N+1 grouping key: the canonical, literal-free query
// fingerprint. It delegates to analyzer.Fingerprint so there is a single
// normalizer in the codebase (the comment/string-literal-aware one) rather
// than a second, subtly different regex pass.
func normalizeQuery(query string) string {
	return analyzer.Fingerprint(query)
}

type queryRecord struct {
	count     int
	firstSeen time.Time
	reported  bool
}

// QueryTracker detects N+1 query patterns at runtime.
// It tracks normalized query patterns and flags when the same pattern
// is executed more than a threshold number of times within a time window.
type QueryTracker struct {
	mu        sync.Mutex
	queries   map[string]*queryRecord
	threshold int
	window    time.Duration
	maxKeys   int
	reporter  func(results []analyzer.Result)
}

// NewQueryTracker creates a tracker that flags when the same query pattern
// appears more than threshold times within the given window.
func NewQueryTracker(threshold int, window time.Duration, reportFn func([]analyzer.Result)) *QueryTracker {
	return &QueryTracker{
		queries:   make(map[string]*queryRecord),
		threshold: threshold,
		window:    window,
		maxKeys:   10000,
		reporter:  reportFn,
	}
}

// Track records a query execution and reports if N+1 pattern is detected.
func (qt *QueryTracker) Track(query string) {
	normalized := normalizeQuery(query)

	qt.mu.Lock()

	now := time.Now()

	// Bound memory: when the map is at capacity, evict expired entries first.
	if len(qt.queries) >= qt.maxKeys {
		qt.evictExpired(now)
	}

	rec, exists := qt.queries[normalized]
	if !exists {
		// A new key past the cap (eviction freed nothing — every entry is still
		// in-window) is dropped rather than grown without bound: a rare,
		// harmless false negative under pathological query-shape cardinality.
		// Already-tracked keys (the exists path below) are always honored, so
		// in-flight N+1 detection is never lost.
		if len(qt.queries) >= qt.maxKeys {
			qt.mu.Unlock()
			return
		}
		qt.queries[normalized] = &queryRecord{count: 1, firstSeen: now}
		qt.mu.Unlock()
		return
	}

	// If outside the window, reset
	if now.Sub(rec.firstSeen) > qt.window {
		rec.count = 1
		rec.firstSeen = now
		rec.reported = false
		qt.mu.Unlock()
		return
	}

	rec.count++

	shouldReport := rec.count >= qt.threshold && !rec.reported
	if shouldReport {
		rec.reported = true
	}

	// Release lock before calling reporter to avoid holding mutex during I/O
	count := rec.count
	qt.mu.Unlock()

	if shouldReport {
		qt.reporter([]analyzer.Result{{
			RuleName:    "n-plus-one",
			Severity:    analyzer.SeverityWarning,
			Query:       normalized,
			Fingerprint: normalized,
			Message:     fmt.Sprintf("Possible N+1 query detected: same pattern executed %d times in %s", count, qt.window),
			Suggestion:  "Consider using a JOIN or IN clause to batch these queries.",
		}})
	}
}

// evictExpired removes entries older than the window. Must be called with mutex held.
func (qt *QueryTracker) evictExpired(now time.Time) {
	for key, rec := range qt.queries {
		if now.Sub(rec.firstSeen) > qt.window {
			delete(qt.queries, key)
		}
	}
}

// Reset clears all tracked queries. Call this between requests.
func (qt *QueryTracker) Reset() {
	qt.mu.Lock()
	defer qt.mu.Unlock()
	qt.queries = make(map[string]*queryRecord)
}
