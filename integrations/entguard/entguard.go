// Package entguard integrates sqlguard with ent (entgo.io/ent).
//
// ent runs on database/sql, so the simplest coverage is already available by
// pointing entsql at a *sql.DB obtained from sqlguard.Register / OpenDB. This
// package is the dedicated alternative: it decorates ent's own
// dialect.Driver seam, so it works regardless of how the underlying *sql.DB
// was opened (including ent's dialect.DebugDriver chain) and mirrors ent's
// built-in dialect.Debug wrapper.
//
// Analysis is driven by the single shared sqlguard core (middleware.Guard),
// so redaction-by-default, stable fingerprints, the pluggable real-grammar
// parser, slow-query timing and N+1 detection behave identically to every
// other sqlguard surface. There is no parallel option surface — configure
// with the standard middleware options:
//
//	drv, _ := entsql.Open(dialect.Postgres, dsn)
//	guarded := entguard.Wrap(drv,
//	    middleware.WithSlowQueryThreshold(500*time.Millisecond),
//	    middleware.WithN1Detection(10, time.Second),
//	)
//	client := ent.NewClient(ent.Driver(guarded))
//
// Every Exec/Query — on the driver and on transactions it opens — flows
// through middleware.Guard.Observe: static rules run on every call, latency
// is recorded only on success.
package entguard

import (
	"context"
	"database/sql"

	"entgo.io/ent/dialect"
	"github.com/KARTIKrocks/sqlguard/middleware"
)

// Driver decorates an ent dialect.Driver, routing every statement through the
// shared sqlguard analysis core.
type Driver struct {
	dialect.Driver
	g *middleware.Guard
}

// Compile-time proof we still satisfy ent's driver contract.
var _ dialect.Driver = (*Driver)(nil)

// Wrap decorates an ent dialect.Driver. It accepts the standard sqlguard
// middleware options (WithAnalyzer, WithReporter, WithSlowQueryThreshold,
// WithParser, WithN1Detection, …) — the same option set every other sqlguard
// surface uses, so there is no parallel configuration surface to drift.
func Wrap(d dialect.Driver, opts ...middleware.Option) *Driver {
	return &Driver{Driver: d, g: middleware.NewGuard(opts...)}
}

// ResetN1 clears N+1 tracker state. Call it at a per-request boundary
// (e.g. end of an HTTP handler) to scope N+1 detection to one unit of work.
// No-op unless WithN1Detection was passed to Wrap.
func (d *Driver) ResetN1() { d.g.ResetN1() }

// Exec implements dialect.ExecQuerier.
func (d *Driver) Exec(ctx context.Context, query string, args, v any) error {
	done := d.g.Observe(query)
	err := d.Driver.Exec(ctx, query, args, v)
	done(err)
	return err
}

// Query implements dialect.ExecQuerier.
func (d *Driver) Query(ctx context.Context, query string, args, v any) error {
	done := d.g.Observe(query)
	err := d.Driver.Query(ctx, query, args, v)
	done(err)
	return err
}

// Tx wraps the transaction so statements executed inside it are analyzed too.
func (d *Driver) Tx(ctx context.Context) (dialect.Tx, error) {
	t, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &tx{Tx: t, g: d.g}, nil
}

// BeginTx forwards to the wrapped driver's BeginTx when it implements one
// (entsql.Driver does — this is how ent honours read-only / isolation
// options), and wraps the resulting transaction. It degrades to Tx when the
// base driver has no BeginTx, matching ent's own fallback.
func (d *Driver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	bt, ok := d.Driver.(interface {
		BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error)
	})
	if !ok {
		return d.Tx(ctx)
	}
	t, err := bt.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &tx{Tx: t, g: d.g}, nil
}

// tx decorates a dialect.Tx so in-transaction Exec/Query are analyzed.
// Commit/Rollback are inherited from the embedded transaction unchanged.
type tx struct {
	dialect.Tx
	g *middleware.Guard
}

// Exec implements dialect.ExecQuerier.
func (t *tx) Exec(ctx context.Context, query string, args, v any) error {
	done := t.g.Observe(query)
	err := t.Tx.Exec(ctx, query, args, v)
	done(err)
	return err
}

// Query implements dialect.ExecQuerier.
func (t *tx) Query(ctx context.Context, query string, args, v any) error {
	done := t.g.Observe(query)
	err := t.Tx.Query(ctx, query, args, v)
	done(err)
	return err
}
