package middleware

import (
	"fmt"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// Guard is the single shared analysis core. It runs the configured analyzer
// and reporter against every executed query, measures latency, and feeds the
// N+1 tracker. Every interception point — the database/sql driver chain and
// every out-of-tree integration (pgxguard, …) — drives the same Guard so
// analysis logic, redaction, fingerprinting, N+1, the parser seam and config
// live here exactly once. Integrations must build on Guard rather than
// re-implementing check/latency by hand (that path silently loses
// redaction-by-default and fingerprints).
//
// A Guard is safe for concurrent use.
type Guard struct {
	opts    options
	tracker *QueryTracker
	deduper *deduper
	cache   *analysisCache
}

// NewGuard builds a Guard from the given options.
func NewGuard(opts ...Option) *Guard {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	if o.parser != nil {
		o.analyzer = o.analyzer.WithParser(o.parser)
	}
	g := &Guard{opts: o, deduper: newDeduper(o.dedupWindow)}
	if o.cacheSize > 0 {
		g.cache = newAnalysisCache(o.cacheSize)
	}
	if o.enableN1 {
		g.tracker = NewQueryTracker(o.n1Threshold, o.n1Window, func(results []analyzer.Result) {
			o.reporter.Report(results)
		})
	}
	return g
}

// Analyzer returns the configured analyzer. Useful for integrations that need
// the canonical redact/fingerprint helpers without re-deriving policy.
func (g *Guard) Analyzer() *analyzer.Analyzer { return g.opts.analyzer }

// Check runs the static rules against the query and feeds the N+1 tracker.
func (g *Guard) Check(query string) {
	results := g.analyze(query)
	if len(results) > 0 {
		g.report(results)
	}
	if g.tracker != nil {
		g.tracker.Track(query)
	}
}

// analyze returns the static findings for query, memoizing per distinct query
// string so a recurring query is parsed and rule-checked once. The cache is
// keyed on the exact query string because a few rules read literal-derived
// facts the fingerprint folds away (see analysisCache). The returned slice may
// be shared from the cache and must be treated as read-only.
func (g *Guard) analyze(query string) []analyzer.Result {
	if g.cache == nil {
		return g.opts.analyzer.Analyze(query)
	}
	if cached, ok := g.cache.get(query); ok {
		return cached
	}
	results := g.opts.analyzer.Analyze(query)
	g.cache.put(query, results)
	return results
}

// report emits static findings, suppressing repeats of the same
// (fingerprint, rule) within the dedup window so a recurring query does not
// flood the reporter. results may be a shared cache entry, so it is never
// mutated; kept is allocated only when a finding actually passes dedup (rare
// after the first occurrence, and never for the common no-findings case).
func (g *Guard) report(results []analyzer.Result) {
	now := time.Now()
	var kept []analyzer.Result
	for _, r := range results {
		if g.deduper.allow(r.Fingerprint, r.RuleName, now) {
			kept = append(kept, r)
		}
	}
	if len(kept) > 0 {
		g.opts.reporter.Report(kept)
	}
}

// CheckLatency reports a slow-query finding if elapsed exceeds the threshold.
func (g *Guard) CheckLatency(query string, elapsed time.Duration) {
	if elapsed >= g.opts.slowThreshold {
		display, fingerprint := g.opts.analyzer.PrepareQuery(query)
		g.opts.reporter.Report([]analyzer.Result{{
			RuleName:    "slow-query",
			Severity:    analyzer.SeverityWarning,
			Query:       display,
			Fingerprint: fingerprint,
			Message:     fmt.Sprintf("Query took %s (threshold: %s)", elapsed.Round(time.Millisecond), g.opts.slowThreshold),
			Suggestion:  "Consider adding indexes or optimizing the query.",
		}})
	}
}

// Observe analyzes a query and times its execution. The returned function
// must be called once the underlying operation completes; it records latency
// only when err is nil (a failed query's latency is meaningless). It is
// designed for split start/end interception points such as pgx tracers:
// call Observe in the start hook, stash the closure, invoke it in the end
// hook with the operation error.
func (g *Guard) Observe(query string) func(err error) {
	g.Check(query)
	start := time.Now()
	return func(err error) {
		if err == nil {
			g.CheckLatency(query, time.Since(start))
		}
	}
}

// ResetN1 clears the N+1 tracker's accumulated state. Call this at a
// per-request boundary (e.g. end of an HTTP handler) so N+1 detection is
// scoped to a single logical unit of work rather than process-global. It is
// a no-op when N+1 detection is not enabled.
func (g *Guard) ResetN1() {
	if g.tracker != nil {
		g.tracker.Reset()
	}
}
