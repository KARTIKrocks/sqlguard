// Package gormguard integrates sqlguard with GORM.
//
// Analysis is driven by the single shared sqlguard core (middleware.Guard),
// so redaction-by-default, stable fingerprints, the pluggable real-grammar
// parser, slow-query timing and N+1 detection behave identically to the
// database/sql driver wrapper and to pgxguard. There is no parallel option
// surface — configure with the standard middleware options:
//
//	gormDB, _ := gorm.Open(postgres.Open(dsn), &gorm.Config{})
//	gormguard.Register(gormDB,
//	    middleware.WithSlowQueryThreshold(500*time.Millisecond),
//	    middleware.WithN1Detection(10, time.Second),
//	)
//
// GORM only exposes the final built SQL in its after-callback (it has not
// been generated when the before-callback fires), so this plugin uses the
// explicit Check+CheckLatency pair rather than middleware.Guard.Observe.
// Behaviour matches Observe semantically: static rules run on every call,
// latency is reported only on success.
package gormguard

import (
	"time"

	"github.com/KARTIKrocks/sqlguard/middleware"
	"gorm.io/gorm"
)

// Plugin implements gorm.Plugin and drives every traced statement through
// the shared sqlguard analysis core.
type Plugin struct {
	g *middleware.Guard
}

// Compile-time proof we satisfy gorm.Plugin.
var _ gorm.Plugin = (*Plugin)(nil)

// New creates a new sqlguard GORM plugin. It accepts the standard sqlguard
// middleware options (WithAnalyzer, WithReporter, WithSlowQueryThreshold,
// WithParser, WithN1Detection, …) — the same option set the database/sql
// driver wrapper and pgxguard use, so there is no parallel configuration
// surface to drift.
func New(opts ...middleware.Option) *Plugin {
	return &Plugin{g: middleware.NewGuard(opts...)}
}

// Name implements gorm.Plugin.
func (p *Plugin) Name() string { return "sqlguard" }

// ResetN1 clears N+1 tracker state. Call it at a per-request boundary
// (e.g. end of an HTTP handler) to scope N+1 detection to one unit of work.
// No-op unless WithN1Detection was passed to New / Register.
func (p *Plugin) ResetN1() { p.g.ResetN1() }

// Initialize registers before/after callbacks on every GORM callback chain.
//
// GORM v2 routes operations through six distinct callback chains:
//   - Create/Update/Delete  — ORM-style mutating operations
//   - Query                 — ORM-style reads (First/Find/Take/…)
//   - Row                   — raw SQL that returns rows (db.Raw().Scan / .Row)
//   - Raw                   — raw SQL without rows (db.Exec)
//
// Missing any chain silently uncovers a query class — pre-rewrite, only
// Create/Query/Update/Delete were hooked, so every db.Raw and db.Exec
// bypassed analysis (and there were no tests to catch it). All six chains
// are now registered.
//
// SQL is analyzed in the after-callback because GORM has not yet rendered
// db.Statement.SQL when the before-callback fires for the ORM chains.
func (p *Plugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	registrations := []struct {
		before, after func(name string, fn func(*gorm.DB)) error
		chain         string
	}{
		{before: cb.Create().Before("gorm:create").Register, after: cb.Create().After("gorm:create").Register, chain: "create"},
		{before: cb.Query().Before("gorm:query").Register, after: cb.Query().After("gorm:query").Register, chain: "query"},
		{before: cb.Update().Before("gorm:update").Register, after: cb.Update().After("gorm:update").Register, chain: "update"},
		{before: cb.Delete().Before("gorm:delete").Register, after: cb.Delete().After("gorm:delete").Register, chain: "delete"},
		{before: cb.Row().Before("gorm:row").Register, after: cb.Row().After("gorm:row").Register, chain: "row"},
		{before: cb.Raw().Before("gorm:raw").Register, after: cb.Raw().After("gorm:raw").Register, chain: "raw"},
	}
	for _, r := range registrations {
		if err := r.before("sqlguard:before_"+r.chain, p.before); err != nil {
			return err
		}
		if err := r.after("sqlguard:after_"+r.chain, p.after); err != nil {
			return err
		}
	}
	return nil
}

// startTimeKey is the per-statement context key under which the before
// callback stashes the start timestamp. Unexported so it can't collide with
// keys set by other plugins.
const startTimeKey = "sqlguard:start_time"

func (p *Plugin) before(db *gorm.DB) {
	db.Set(startTimeKey, time.Now())
}

func (p *Plugin) after(db *gorm.DB) {
	if db.Statement == nil {
		return
	}
	sql := db.Statement.SQL.String()
	if sql == "" {
		return
	}

	// Static rules + N+1 run on every call (matches Observe semantics).
	p.g.Check(sql)

	// Latency is reported only on success — a failed query's duration is
	// meaningless. This mirrors middleware.Guard.Observe.
	if db.Error != nil {
		return
	}
	val, ok := db.Get(startTimeKey)
	if !ok {
		return
	}
	start, ok := val.(time.Time)
	if !ok {
		return
	}
	p.g.CheckLatency(sql, time.Since(start))
}

// Register is a convenience function to create and register the plugin.
func Register(db *gorm.DB, opts ...middleware.Option) error {
	return db.Use(New(opts...))
}
