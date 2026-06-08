// Package pgxguard integrates sqlguard with pgx/v5 — the native, dominant
// PostgreSQL driver for Go (pgx/pgxpool, not the database/sql shim).
//
// It hooks pgx's own tracer seam (pgx.QueryTracer + pgx.BatchTracer), which
// is the idiomatic extension point every pgx ecosystem tool uses, so every
// Query/QueryRow/Exec and every SendBatch is analyzed without a method list
// or a wrapper type.
//
// Composability is a first-class concern: pgx allows exactly one Tracer per
// config, and production services usually already set one (otelpgx). Apply
// and ApplyPool therefore *compose* with any existing tracer via pgx's own
// multitracer rather than overwriting it.
//
// Usage with a pool:
//
//	cfg, _ := pgxpool.ParseConfig(dsn)
//	pgxguard.ApplyPool(cfg) // composes with cfg.ConnConfig.Tracer if set
//	pool, _ := pgxpool.NewWithConfig(ctx, cfg)
//
// Usage with a single connection:
//
//	cfg, _ := pgx.ParseConfig(dsn)
//	pgxguard.Apply(cfg)
//	conn, _ := pgx.ConnectConfig(ctx, cfg)
//
// Analysis is driven by the single sqlguard core (middleware.Guard), so
// redaction-by-default, stable fingerprints, the pluggable real-grammar
// parser, slow-query and N+1 detection all behave identically to the
// database/sql driver wrapper. Configure with the standard middleware
// options:
//
//	pgxguard.NewTracer(
//	    middleware.WithSlowQueryThreshold(50*time.Millisecond),
//	    middleware.WithN1Detection(10, time.Second),
//	)
package pgxguard

import (
	"context"

	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tracer implements pgx.QueryTracer and pgx.BatchTracer, driving every
// traced statement through the shared sqlguard analysis core.
//
// It deliberately does not implement pgx.PrepareTracer: prepared statements
// are still analyzed when executed (execution routes through QueryTracer),
// so tracing Prepare as well would double-report findings and inflate N+1
// counts. CopyFrom carries no SQL and is out of scope by nature.
type Tracer struct {
	g *middleware.Guard
}

// Compile-time proof we satisfy the pgx tracer interfaces we claim.
var (
	_ pgx.QueryTracer = (*Tracer)(nil)
	_ pgx.BatchTracer = (*Tracer)(nil)
)

// NewTracer builds a Tracer. It accepts the standard sqlguard middleware
// options (WithAnalyzer, WithReporter, WithSlowQueryThreshold, WithParser,
// WithN1Detection, …) — the same option set the database/sql driver wrapper
// uses, so there is no parallel configuration surface to drift.
func NewTracer(opts ...middleware.Option) *Tracer {
	return &Tracer{g: middleware.NewGuard(opts...)}
}

// ResetN1 clears N+1 tracker state. Call it at a per-request boundary
// (e.g. end of an HTTP handler) to scope N+1 detection to one unit of work.
// No-op unless WithN1Detection was passed to NewTracer.
func (t *Tracer) ResetN1() { t.g.ResetN1() }

// ctxKey is unexported so the stashed latency closure can't collide with
// any other package's context values.
type ctxKey struct{}

// TraceQueryStart runs static analysis + N+1 tracking and starts the latency
// timer, stashing the end closure in the returned context.
func (t *Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	done := t.g.Observe(data.SQL)
	return context.WithValue(ctx, ctxKey{}, done)
}

// TraceQueryEnd closes the latency window. Latency is recorded only on
// success (Guard.Observe drops it when data.Err != nil — a failed query's
// duration is meaningless).
func (t *Tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if done, ok := ctx.Value(ctxKey{}).(func(error)); ok {
		done(data.Err)
	}
}

// TraceBatchStart is a no-op: the batch's SQL is only known per-query, in
// TraceBatchQuery.
func (t *Tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}

// TraceBatchQuery analyzes each statement in a batch (static rules + N+1).
// Per-statement latency is not exposed by pgx's batch tracer — only the
// whole-batch round trip — so slow-query timing is intentionally not
// reported here rather than reported wrongly.
func (t *Tracer) TraceBatchQuery(_ context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	t.g.Check(data.SQL)
}

// TraceBatchEnd is a no-op (per-statement analysis happens in TraceBatchQuery).
func (t *Tracer) TraceBatchEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceBatchEndData) {}

// Apply installs a sqlguard Tracer on a *pgx.ConnConfig, composing with any
// tracer already configured (via pgx's multitracer) instead of overwriting
// it — so it coexists with otelpgx and friends. opts are the standard
// middleware options. Returns the same cfg for chaining.
func Apply(cfg *pgx.ConnConfig, opts ...middleware.Option) *pgx.ConnConfig {
	if cfg == nil {
		panic("pgxguard: Apply called with nil *pgx.ConnConfig")
	}
	cfg.Tracer = compose(cfg.Tracer, NewTracer(opts...))
	return cfg
}

// ApplyPool installs a sqlguard Tracer on a *pgxpool.Config (delegating to
// Apply on the embedded ConnConfig), composing with any existing tracer.
// Returns the same cfg for chaining.
func ApplyPool(cfg *pgxpool.Config, opts ...middleware.Option) *pgxpool.Config {
	if cfg == nil {
		panic("pgxguard: ApplyPool called with nil *pgxpool.Config")
	}
	Apply(cfg.ConnConfig, opts...)
	return cfg
}

// compose merges an existing tracer with ours. multitracer.New fans each
// call out to every wrapped tracer and routes by interface type-assertion,
// so the existing tracer keeps receiving exactly the events it did before.
func compose(existing pgx.QueryTracer, ours pgx.QueryTracer) pgx.QueryTracer {
	if existing == nil {
		return ours
	}
	return multitracer.New(existing, ours)
}
