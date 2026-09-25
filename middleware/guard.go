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
	// slowQuery is the profile's resolved decision for the `slow-query`
	// rule. Resolved once here because Guard runs on every query.
	slowQuery findingPolicy
}

// findingPolicy is a registered rule's resolved state for a finding the
// analyzer does not evaluate itself. `enabled` folds in `disable` and `only`;
// `severity` folds in a profile override.
type findingPolicy struct {
	enabled  bool
	severity analyzer.Severity
}

// resolvePolicy reads the default severity from the registry rather than
// repeating a literal here, so `Register(RuleSpec{Name: "slow-query", …})` is
// what decides it — the same as for an evaluated rule.
func resolvePolicy(a *analyzer.Analyzer, name string) findingPolicy {
	def := analyzer.RuleDefaultSeverityOr(name, analyzer.SeverityWarning)
	return findingPolicy{
		enabled:  a.RuleEnabled(name),
		severity: a.RuleSeverity(name, def),
	}
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
	// Profile settings feed the thresholds unless a Go option named one
	// explicitly, so `rules.settings` works the same for these findings as
	// for any statement rule.
	if !o.slowThresholdSet {
		o.slowThreshold = o.analyzer.RuleSettings("slow-query").
			Duration("threshold", o.slowThreshold)
	}
	if !o.n1Set {
		if s := o.analyzer.RuleSettings("n-plus-one"); s != nil {
			threshold := s.Int("threshold", 0)
			window := s.Duration("window", 0)
			if threshold > 0 && window > 0 {
				o.enableN1, o.n1Threshold, o.n1Window = true, threshold, window
			}
		}
	}

	g := &Guard{opts: o, deduper: newDeduper(o.dedupWindow)}
	g.slowQuery = resolvePolicy(o.analyzer, "slow-query")
	if o.cacheSize > 0 {
		g.cache = newAnalysisCache(o.cacheSize)
	}
	n1 := resolvePolicy(o.analyzer, "n-plus-one")
	if o.enableN1 && n1.enabled {
		g.tracker = NewQueryTracker(o.n1Threshold, o.n1Window, n1.severity, func(results []analyzer.Result) {
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
// Does nothing when the profile disabled `slow-query`.
func (g *Guard) CheckLatency(query string, elapsed time.Duration) {
	if g.slowQuery.enabled && elapsed >= g.opts.slowThreshold {
		display, fingerprint := g.opts.analyzer.PrepareQuery(query)
		g.opts.reporter.Report([]analyzer.Result{{
			RuleName:    "slow-query",
			Severity:    g.slowQuery.severity,
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
