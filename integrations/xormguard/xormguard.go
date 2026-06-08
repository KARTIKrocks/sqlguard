// Package xormguard integrates sqlguard with xorm (xorm.io/xorm).
//
// Analysis is driven by the single shared sqlguard core (middleware.Guard),
// so redaction-by-default, stable fingerprints, the pluggable real-grammar
// parser, slow-query timing and N+1 detection behave identically to the
// database/sql driver wrapper, pgxguard, gormguard and bunguard. There is no
// parallel option surface — configure with the standard middleware options:
//
//	engine, _ := xorm.NewEngine("postgres", dsn)
//	engine.AddHook(xormguard.New(
//	    middleware.WithSlowQueryThreshold(500*time.Millisecond),
//	    middleware.WithN1Detection(10, time.Second),
//	))
//
// xorm's contexts.Hook exposes the rendered SQL and the measured execution
// time on the ContextHook in AfterProcess, so this uses the explicit
// Check+CheckLatency pair (matching gormguard): static rules run on every
// query, latency is reported only on success.
package xormguard

import (
	"context"

	"github.com/KARTIKrocks/sqlguard/middleware"
	"xorm.io/xorm/contexts"
)

// Hook implements xorm's contexts.Hook and drives every traced statement
// through the shared sqlguard analysis core.
type Hook struct {
	g *middleware.Guard
}

// Compile-time proof we satisfy contexts.Hook.
var _ contexts.Hook = (*Hook)(nil)

// New creates a new sqlguard xorm hook. It accepts the standard sqlguard
// middleware options (WithAnalyzer, WithReporter, WithSlowQueryThreshold,
// WithParser, WithN1Detection, …) — the same option set every other sqlguard
// surface uses, so there is no parallel configuration surface to drift.
func New(opts ...middleware.Option) *Hook {
	return &Hook{g: middleware.NewGuard(opts...)}
}

// ResetN1 clears N+1 tracker state. Call it at a per-request boundary
// (e.g. end of an HTTP handler) to scope N+1 detection to one unit of work.
// No-op unless WithN1Detection was passed to New.
func (h *Hook) ResetN1() { h.g.ResetN1() }

// BeforeProcess implements contexts.Hook. xorm stamps the start time itself
// and reports the elapsed duration as ContextHook.ExecuteTime in
// AfterProcess, so there is nothing to do here but pass the context through.
func (h *Hook) BeforeProcess(c *contexts.ContextHook) (context.Context, error) {
	return c.Ctx, nil
}

// AfterProcess implements contexts.Hook. c.SQL holds the rendered SQL,
// c.ExecuteTime the measured latency, and c.Err the query error (which is
// returned unchanged so the hook never swallows it).
func (h *Hook) AfterProcess(c *contexts.ContextHook) error {
	if c.SQL == "" {
		return c.Err
	}

	// Static rules + N+1 run on every call (matches Observe semantics).
	h.g.Check(c.SQL)

	// Latency is reported only on success — a failed query's duration is
	// meaningless. This mirrors middleware.Guard.Observe.
	if c.Err == nil {
		h.g.CheckLatency(c.SQL, c.ExecuteTime)
	}
	return c.Err
}
