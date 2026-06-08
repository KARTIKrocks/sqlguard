// Package bunguard integrates sqlguard with bun (github.com/uptrace/bun).
//
// Analysis is driven by the single shared sqlguard core (middleware.Guard),
// so redaction-by-default, stable fingerprints, the pluggable real-grammar
// parser, slow-query timing and N+1 detection behave identically to the
// database/sql driver wrapper, pgxguard and gormguard. There is no parallel
// option surface — configure with the standard middleware options:
//
//	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
//	db := bun.NewDB(sqldb, pgdialect.New())
//	db.AddQueryHook(bunguard.New(
//	    middleware.WithSlowQueryThreshold(500*time.Millisecond),
//	    middleware.WithN1Detection(10, time.Second),
//	))
//
// bun exposes the final rendered SQL and a start timestamp on the QueryEvent
// in its AfterQuery hook, so this uses the explicit Check+CheckLatency pair
// (matching gormguard) rather than middleware.Guard.Observe: static rules run
// on every query, latency is reported only on success.
package bunguard

import (
	"context"
	"time"

	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/uptrace/bun"
)

// QueryHook implements bun.QueryHook and drives every traced statement
// through the shared sqlguard analysis core.
type QueryHook struct {
	g *middleware.Guard
}

// Compile-time proof we satisfy bun.QueryHook.
var _ bun.QueryHook = (*QueryHook)(nil)

// New creates a new sqlguard bun query hook. It accepts the standard sqlguard
// middleware options (WithAnalyzer, WithReporter, WithSlowQueryThreshold,
// WithParser, WithN1Detection, …) — the same option set the database/sql
// driver wrapper, pgxguard and gormguard use, so there is no parallel
// configuration surface to drift.
func New(opts ...middleware.Option) *QueryHook {
	return &QueryHook{g: middleware.NewGuard(opts...)}
}

// ResetN1 clears N+1 tracker state. Call it at a per-request boundary
// (e.g. end of an HTTP handler) to scope N+1 detection to one unit of work.
// No-op unless WithN1Detection was passed to New.
func (h *QueryHook) ResetN1() { h.g.ResetN1() }

// BeforeQuery implements bun.QueryHook. bun stamps event.StartTime itself
// before invoking the hook, so there is nothing to stash here.
func (h *QueryHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery implements bun.QueryHook. event.Query holds the rendered SQL.
func (h *QueryHook) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	sql := event.Query
	if sql == "" {
		return
	}

	// Static rules + N+1 run on every call (matches Observe semantics).
	h.g.Check(sql)

	// Latency is reported only on success — a failed query's duration is
	// meaningless. This mirrors middleware.Guard.Observe.
	if event.Err != nil {
		return
	}
	h.g.CheckLatency(sql, time.Since(event.StartTime))
}
