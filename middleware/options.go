package middleware

import (
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/reporter"
)

type options struct {
	slowThreshold time.Duration
	reporter      reporter.Reporter
	analyzer      *analyzer.Analyzer
	parser        analyzer.Parser
	n1Threshold   int
	n1Window      time.Duration
	enableN1      bool
	dedupWindow   time.Duration
	cacheSize     int
}

// Option configures the runtime guard.
type Option func(*options)

// WithSlowQueryThreshold sets the duration above which a query is flagged as slow.
// Default is 200ms.
func WithSlowQueryThreshold(d time.Duration) Option {
	return func(o *options) {
		o.slowThreshold = d
	}
}

// WithReporter sets a custom reporter. Default is ConsoleReporter.
func WithReporter(r reporter.Reporter) Option {
	return func(o *options) {
		o.reporter = r
	}
}

// WithAnalyzer sets a custom analyzer. Default is analyzer.Default().
func WithAnalyzer(a *analyzer.Analyzer) Option {
	return func(o *options) {
		o.analyzer = a
	}
}

// WithParser sets the SQL parser the analyzer uses. Default is the
// zero-dependency analyzer.FallbackParser. Pass a real dialect parser
// (e.g. from sqlguard/parsers/pgparser) for exact, structural analysis.
func WithParser(p analyzer.Parser) Option {
	return func(o *options) {
		o.parser = p
	}
}

// WithN1Detection enables N+1 query detection with the given threshold and window.
// When the same query pattern is executed threshold times within window, a warning is reported.
func WithN1Detection(threshold int, window time.Duration) Option {
	return func(o *options) {
		o.enableN1 = true
		o.n1Threshold = threshold
		o.n1Window = window
	}
}

// WithFindingDedup sets the window within which a repeated static finding —
// the same rule firing on the same canonical query shape — is reported at most
// once. This keeps a recurring query (or a prepared statement run in a loop)
// from flooding the log sink with the same warning on every execution. The
// default is one minute. Pass 0 to disable dedup and report every occurrence
// (the legacy behavior). Slow-query and N+1 findings have their own emission
// policy and are unaffected.
func WithFindingDedup(window time.Duration) Option {
	return func(o *options) {
		o.dedupWindow = window
	}
}

// WithAnalysisCacheSize sets the maximum number of distinct query strings whose
// analysis results are memoized, so a recurring query is parsed and rule-checked
// once instead of on every execution. The cache is an LRU keyed on the exact
// query string (correct even for the literal-sensitive rules). Default is 1024.
// Pass 0 to disable the cache and analyze every query.
func WithAnalysisCacheSize(n int) Option {
	return func(o *options) {
		o.cacheSize = n
	}
}

func defaultOptions() options {
	return options{
		slowThreshold: 200 * time.Millisecond,
		reporter:      reporter.NewConsoleReporter(),
		analyzer:      analyzer.Default(),
		dedupWindow:   time.Minute,
		cacheSize:     1024,
	}
}
