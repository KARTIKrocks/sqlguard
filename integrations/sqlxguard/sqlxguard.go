// Package sqlxguard integrates sqlguard with sqlx.
//
// Every wrapped method routes through the shared sqlguard analysis core
// (middleware.Guard), so redaction-by-default, stable fingerprints, the
// pluggable real-grammar parser, slow-query timing and N+1 detection behave
// identically to the database/sql driver wrapper and to pgxguard. There is
// no parallel option surface — configure with the standard middleware
// options:
//
//	db := sqlxguard.WrapSqlx(sqlxDB,
//	    middleware.WithSlowQueryThreshold(50*time.Millisecond),
//	    middleware.WithN1Detection(10, time.Second),
//	)
//
// Coverage note: WrappedDB exposes the sqlx-specific extension methods
// (Select/Get/Queryx/NamedExec and their *Context variants, plus Query/Exec
// passthrough). For full surface coverage — including QueryRow*, NamedQuery,
// MustExec and the transaction helpers — layer sqlx on top of the sqlguard
// driver chain instead:
//
//	sqlguard.Register("sqlguard-pgx", pq.Driver{}, opts...)
//	sqlDB, _ := sql.Open("sqlguard-pgx", dsn)
//	db := sqlx.NewDb(sqlDB, "postgres")
//
// That path covers every sqlx method automatically because interception
// happens at the database/sql driver layer.
package sqlxguard

import (
	"context"
	"database/sql"

	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/jmoiron/sqlx"
)

// WrappedDB wraps a *sqlx.DB with sqlguard analysis. Every analysis-bearing
// method drives the shared middleware.Guard, so behavior matches pgxguard
// and the database/sql driver chain exactly.
type WrappedDB struct {
	db *sqlx.DB
	g  *middleware.Guard
}

// WrapSqlx creates a new WrappedDB around the given sqlx connection.
// It accepts the standard sqlguard middleware options (WithAnalyzer,
// WithReporter, WithSlowQueryThreshold, WithParser, WithN1Detection, …) —
// the same option set the database/sql driver wrapper and pgxguard use, so
// there is no parallel configuration surface to drift.
func WrapSqlx(db *sqlx.DB, opts ...middleware.Option) *WrappedDB {
	if db == nil {
		panic("sqlxguard: WrapSqlx called with nil *sqlx.DB")
	}
	return &WrappedDB{db: db, g: middleware.NewGuard(opts...)}
}

// DB returns the underlying *sqlx.DB.
func (w *WrappedDB) DB() *sqlx.DB { return w.db }

// ResetN1 clears N+1 tracker state. Call it at a per-request boundary
// (e.g. end of an HTTP handler) to scope N+1 detection to one unit of work.
// No-op unless WithN1Detection was passed to WrapSqlx.
func (w *WrappedDB) ResetN1() { w.g.ResetN1() }

// Select executes a query and scans the results into dest.
func (w *WrappedDB) Select(dest any, query string, args ...any) error {
	done := w.g.Observe(query)
	err := w.db.Select(dest, query, args...)
	done(err)
	return err
}

// SelectContext executes a query with context and scans the results into dest.
func (w *WrappedDB) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	done := w.g.Observe(query)
	err := w.db.SelectContext(ctx, dest, query, args...)
	done(err)
	return err
}

// Get executes a query and scans a single row into dest.
func (w *WrappedDB) Get(dest any, query string, args ...any) error {
	done := w.g.Observe(query)
	err := w.db.Get(dest, query, args...)
	done(err)
	return err
}

// GetContext executes a query with context and scans a single row into dest.
func (w *WrappedDB) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	done := w.g.Observe(query)
	err := w.db.GetContext(ctx, dest, query, args...)
	done(err)
	return err
}

// Query executes a query that returns rows.
func (w *WrappedDB) Query(query string, args ...any) (*sql.Rows, error) {
	done := w.g.Observe(query)
	rows, err := w.db.Query(query, args...)
	done(err)
	return rows, err
}

// QueryContext executes a query with context that returns rows.
func (w *WrappedDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	done := w.g.Observe(query)
	rows, err := w.db.QueryContext(ctx, query, args...)
	done(err)
	return rows, err
}

// Queryx executes a query that returns sqlx.Rows.
func (w *WrappedDB) Queryx(query string, args ...any) (*sqlx.Rows, error) {
	done := w.g.Observe(query)
	rows, err := w.db.Queryx(query, args...)
	done(err)
	return rows, err
}

// Exec executes a query without returning rows.
func (w *WrappedDB) Exec(query string, args ...any) (sql.Result, error) {
	done := w.g.Observe(query)
	result, err := w.db.Exec(query, args...)
	done(err)
	return result, err
}

// ExecContext executes a query with context without returning rows.
func (w *WrappedDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	done := w.g.Observe(query)
	result, err := w.db.ExecContext(ctx, query, args...)
	done(err)
	return result, err
}

// NamedExec executes a named query.
func (w *WrappedDB) NamedExec(query string, arg any) (sql.Result, error) {
	done := w.g.Observe(query)
	result, err := w.db.NamedExec(query, arg)
	done(err)
	return result, err
}

// NamedExecContext executes a named query with context.
func (w *WrappedDB) NamedExecContext(ctx context.Context, query string, arg any) (sql.Result, error) {
	done := w.g.Observe(query)
	result, err := w.db.NamedExecContext(ctx, query, arg)
	done(err)
	return result, err
}

// Ping verifies the database connection.
func (w *WrappedDB) Ping() error { return w.db.Ping() }

// Close closes the database connection.
func (w *WrappedDB) Close() error { return w.db.Close() }
