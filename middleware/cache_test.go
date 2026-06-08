package middleware

import (
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestAnalysisCache_HitMissAndStore(t *testing.T) {
	c := newAnalysisCache(4)

	if _, ok := c.get("q"); ok {
		t.Fatal("empty cache should miss")
	}

	res := []analyzer.Result{{RuleName: "select-star"}}
	c.put("q", res)

	got, ok := c.get("q")
	if !ok {
		t.Fatal("expected a hit after put")
	}
	if len(got) != 1 || got[0].RuleName != "select-star" {
		t.Errorf("cached results mismatch: %+v", got)
	}
}

func TestAnalysisCache_CachesEmptyResults(t *testing.T) {
	c := newAnalysisCache(4)
	c.put("clean", nil) // a query that produced no findings is still worth caching

	got, ok := c.get("clean")
	if !ok {
		t.Fatal("a cached no-findings query must be a hit, not a miss")
	}
	if len(got) != 0 {
		t.Errorf("expected zero findings, got %d", len(got))
	}
}

func TestAnalysisCache_LRUEviction(t *testing.T) {
	c := newAnalysisCache(2)
	c.put("a", nil)
	c.put("b", nil)
	// Touch "a" so "b" becomes least-recently-used.
	if _, ok := c.get("a"); !ok {
		t.Fatal("a should still be present")
	}
	c.put("c", nil) // exceeds capacity -> evict LRU ("b")

	if _, ok := c.get("b"); ok {
		t.Error("b should have been evicted as least-recently-used")
	}
	if _, ok := c.get("a"); !ok {
		t.Error("a should survive (recently used)")
	}
	if _, ok := c.get("c"); !ok {
		t.Error("c should be present (just added)")
	}
	if c.len() > 2 {
		t.Errorf("cache exceeded capacity: %d", c.len())
	}
}

// The cache must not change which findings are produced, including for the
// literal-sensitive rules whose verdict the fingerprint would have folded away.
func TestGuard_CacheCorrectForLiteralSensitiveRules(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithReporter(rep), WithFindingDedup(0)) // dedup off to count every finding

	// Same fingerprint ("... OFFSET ?"), different OffsetValue: only the first
	// crosses the large-offset threshold (default 1000). WHERE + LIMIT keep the
	// only finding large-offset. If the cache keyed on fingerprint, the second
	// would wrongly inherit the first's finding.
	g.Check("SELECT id FROM users WHERE tenant = ? ORDER BY id LIMIT 10 OFFSET 5000")
	if rep.count() != 1 {
		t.Fatalf("expected exactly large-offset on OFFSET 5000, got %d findings", rep.count())
	}
	g.Check("SELECT id FROM users WHERE tenant = ? ORDER BY id LIMIT 10 OFFSET 10")
	if rep.count() != 1 {
		t.Errorf("OFFSET 10 must not inherit a cached large-offset finding; total findings = %d", rep.count())
	}
}

func TestGuard_CacheReturnsConsistentFindingsOnRepeat(t *testing.T) {
	rep := &countingReporter{}
	g := NewGuard(WithReporter(rep), WithFindingDedup(0))

	for range 5 {
		g.Check("DELETE FROM accounts")
	}
	// Cache must not swallow findings: dedup is off, so all 5 are reported.
	if got := rep.count(); got != 5 {
		t.Errorf("expected 5 findings across 5 identical calls, got %d", got)
	}
}

func TestGuard_CacheDisabled(t *testing.T) {
	g := NewGuard(WithAnalysisCacheSize(0))
	if g.cache != nil {
		t.Error("cache size 0 should leave the cache nil (disabled)")
	}
	// Still functions without a cache.
	g.Check("DELETE FROM accounts")
}

// benchQuery is a clean, parameterized query: representative of the prod-common
// case and produces no findings, so Check reduces to the analyze path.
const benchQuery = "SELECT id, name FROM users WHERE id = ? AND tenant = ?"

func BenchmarkGuardCheck_Cached(b *testing.B) {
	g := NewGuard(WithReporter(&countingReporter{}), WithFindingDedup(0))
	b.ReportAllocs()
	for b.Loop() {
		g.Check(benchQuery)
	}
}

func BenchmarkGuardCheck_Uncached(b *testing.B) {
	g := NewGuard(WithReporter(&countingReporter{}), WithFindingDedup(0), WithAnalysisCacheSize(0))
	b.ReportAllocs()
	for b.Loop() {
		g.Check(benchQuery)
	}
}
