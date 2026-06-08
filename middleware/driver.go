package middleware

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
)

// This file implements the standard database/sql driver-wrapping pattern
// (the approach used by ngrok/sqlmw, luna-duclos/instrumentedsql and
// OpenTelemetry's otelsql), hand-written with zero dependencies.
//
// Wrapping at the driver.Driver layer means every query — including those
// issued by ORMs and query builders through database/sql internals — flows
// through the analyzer automatically. There is no method list to keep in
// sync with database/sql, and the result is a real *sql.DB that composes
// with sqlc, ent, sqlx, gorm, pgx-stdlib and anything else.
//
// Optional driver interfaces (QueryerContext, Pinger, SessionResetter, …)
// are forwarded only when the wrapped driver implements them. Because the
// wrapper type structurally implements every optional interface, database/sql
// will always call them; the wrapper returns driver.ErrSkip (or the documented
// no-op) when the base does not support an operation, so database/sql falls
// back exactly as it would for the bare driver. This preserves the base
// driver's behavior without the combinatorial type-switch other libraries use.

// Register wraps the database/sql driver currently registered under
// baseDriver and registers the analyzed result under name. Afterwards
// sql.Open(name, dsn) yields a *sql.DB whose every query is analyzed.
//
//	middleware.Register("sqlguard-sqlite", "sqlite3")
//	db, _ := sql.Open("sqlguard-sqlite", ":memory:")
//
// It returns an error if name is already registered or baseDriver is not
// a known driver.
func Register(name, baseDriver string, opts ...Option) (err error) {
	// sql.Open does not connect; it only resolves the registered driver,
	// so this is a cheap way to obtain the base driver.Driver by name.
	probe, oerr := sql.Open(baseDriver, "")
	if oerr != nil {
		return fmt.Errorf("sqlguard: base driver %q: %w", baseDriver, oerr)
	}
	base := probe.Driver()
	_ = probe.Close()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("sqlguard: register %q: %v", name, r)
		}
	}()
	sql.Register(name, WrapDriver(base, opts...))
	return nil
}

// OpenDB wraps a driver.Connector and returns an analyzed *sql.DB. Use this
// when you already hold a connector — for example pgx's stdlib.GetConnector
// or a driver-specific Connector — and don't want a global registration.
//
//	connector := stdlib.GetConnector(*pgxConfig)
//	db := middleware.OpenDB(connector)
func OpenDB(c driver.Connector, opts ...Option) *sql.DB {
	return sql.OpenDB(WrapConnector(c, opts...))
}

// WrapDriver returns a driver.Driver that analyzes every query executed
// through it. The returned driver also implements driver.DriverContext so
// connector-based pooling is preserved.
func WrapDriver(base driver.Driver, opts ...Option) driver.Driver {
	return &wDriver{base: base, g: NewGuard(opts...)}
}

// WrapConnector returns a driver.Connector that analyzes every query
// executed through connections it produces.
func WrapConnector(base driver.Connector, opts ...Option) driver.Connector {
	return &wConnector{base: base, g: NewGuard(opts...)}
}

// ---- driver.Driver / driver.DriverContext ----

type wDriver struct {
	base driver.Driver
	g    *Guard
}

var (
	_ driver.Driver        = (*wDriver)(nil)
	_ driver.DriverContext = (*wDriver)(nil)
)

func (d *wDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &wConn{base: c, g: d.g}, nil
}

// OpenConnector implements driver.DriverContext. If the base driver supports
// connectors we wrap its connector; otherwise we synthesize a DSN-based
// connector equivalent to the one database/sql builds internally.
func (d *wDriver) OpenConnector(name string) (driver.Connector, error) {
	if dc, ok := d.base.(driver.DriverContext); ok {
		bc, err := dc.OpenConnector(name)
		if err != nil {
			return nil, err
		}
		return &wConnector{base: bc, g: d.g}, nil
	}
	return &wConnector{base: dsnConnector{dsn: name, driver: d.base}, g: d.g}, nil
}

// dsnConnector mirrors database/sql's internal dsnConnector for base drivers
// that do not implement driver.DriverContext.
type dsnConnector struct {
	dsn    string
	driver driver.Driver
}

func (c dsnConnector) Connect(_ context.Context) (driver.Conn, error) {
	return c.driver.Open(c.dsn)
}
func (c dsnConnector) Driver() driver.Driver { return c.driver }

// ---- driver.Connector ----

type wConnector struct {
	base driver.Connector
	g    *Guard
}

var _ driver.Connector = (*wConnector)(nil)

func (c *wConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &wConn{base: conn, g: c.g}, nil
}

func (c *wConnector) Driver() driver.Driver {
	return &wDriver{base: c.base.Driver(), g: c.g}
}

// ---- driver.Conn and its optional interfaces ----

type wConn struct {
	base driver.Conn
	g    *Guard
}

var (
	_ driver.Conn               = (*wConn)(nil)
	_ driver.ConnPrepareContext = (*wConn)(nil)
	_ driver.ConnBeginTx        = (*wConn)(nil)
	_ driver.QueryerContext     = (*wConn)(nil)
	_ driver.ExecerContext      = (*wConn)(nil)
	_ driver.Pinger             = (*wConn)(nil)
	_ driver.SessionResetter    = (*wConn)(nil)
	_ driver.Validator          = (*wConn)(nil)
	_ driver.NamedValueChecker  = (*wConn)(nil)
)

func (c *wConn) Prepare(query string) (driver.Stmt, error) {
	s, err := c.base.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &wStmt{base: s, query: query, g: c.g}, nil
}

func (c *wConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var (
		s   driver.Stmt
		err error
	)
	if cpc, ok := c.base.(driver.ConnPrepareContext); ok {
		s, err = cpc.PrepareContext(ctx, query)
	} else {
		s, err = c.base.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &wStmt{base: s, query: query, g: c.g}, nil
}

func (c *wConn) Close() error { return c.base.Close() }

func (c *wConn) Begin() (driver.Tx, error) {
	tx, err := c.base.Begin() //nolint:staticcheck // delegated deprecated path
	if err != nil {
		return nil, err
	}
	return &wTx{base: tx}, nil
}

func (c *wConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var (
		tx  driver.Tx
		err error
	)
	if cbt, ok := c.base.(driver.ConnBeginTx); ok {
		tx, err = cbt.BeginTx(ctx, opts)
	} else {
		tx, err = c.base.Begin() //nolint:staticcheck // delegated deprecated path
	}
	if err != nil {
		return nil, err
	}
	return &wTx{base: tx}, nil
}

func (c *wConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	// Observe only on a path that actually executes. When the base has no direct
	// Query entry point we return driver.ErrSkip *without* analyzing, so
	// database/sql's Prepare+Query fallback — which re-enters through wStmt — is
	// the single place this query is analyzed. Analyzing here too would count
	// the same logical query twice (a duplicate finding and an inflated N+1).
	if qc, ok := c.base.(driver.QueryerContext); ok {
		done := c.g.Observe(query)
		rows, err := qc.QueryContext(ctx, query, args)
		done(err)
		return rows, err
	}
	if q, ok := c.base.(driver.Queryer); ok { //nolint:staticcheck // legacy fallback
		values, verr := namedToValues(args)
		if verr != nil {
			return nil, verr
		}
		done := c.g.Observe(query)
		rows, err := q.Query(query, values) //nolint:staticcheck // legacy fallback
		done(err)
		return rows, err
	}
	return nil, driver.ErrSkip
}

func (c *wConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	// See QueryContext: analyze only when this path executes. Returning ErrSkip
	// without analyzing lets the Prepare+Exec fallback (via wStmt) be the single
	// analysis point, avoiding a double count.
	if ec, ok := c.base.(driver.ExecerContext); ok {
		done := c.g.Observe(query)
		res, err := ec.ExecContext(ctx, query, args)
		done(err)
		return res, err
	}
	if e, ok := c.base.(driver.Execer); ok { //nolint:staticcheck // legacy fallback
		values, verr := namedToValues(args)
		if verr != nil {
			return nil, verr
		}
		done := c.g.Observe(query)
		res, err := e.Exec(query, values) //nolint:staticcheck // legacy fallback
		done(err)
		return res, err
	}
	return nil, driver.ErrSkip
}

func (c *wConn) Ping(ctx context.Context) error {
	if p, ok := c.base.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	// Base is not a Pinger; ErrSkip tells database/sql ping is unsupported
	// and the connection should be assumed valid, matching the bare driver.
	return driver.ErrSkip
}

func (c *wConn) ResetSession(ctx context.Context) error {
	if r, ok := c.base.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *wConn) IsValid() bool {
	if v, ok := c.base.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *wConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ck, ok := c.base.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(nv)
	}
	// Defer to database/sql's default argument conversion.
	return driver.ErrSkip
}

// ---- driver.Stmt and its optional interfaces ----

type wStmt struct {
	base  driver.Stmt
	query string
	g     *Guard
}

var (
	_ driver.Stmt              = (*wStmt)(nil)
	_ driver.StmtExecContext   = (*wStmt)(nil)
	_ driver.StmtQueryContext  = (*wStmt)(nil)
	_ driver.NamedValueChecker = (*wStmt)(nil)
)

func (s *wStmt) Close() error  { return s.base.Close() }
func (s *wStmt) NumInput() int { return s.base.NumInput() }

func (s *wStmt) Exec(args []driver.Value) (driver.Result, error) {
	done := s.g.Observe(s.query)
	res, err := s.base.Exec(args) //nolint:staticcheck // delegated deprecated path
	done(err)
	return res, err
}

func (s *wStmt) Query(args []driver.Value) (driver.Rows, error) {
	done := s.g.Observe(s.query)
	rows, err := s.base.Query(args) //nolint:staticcheck // delegated deprecated path
	done(err)
	return rows, err
}

func (s *wStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	done := s.g.Observe(s.query)
	if ec, ok := s.base.(driver.StmtExecContext); ok {
		res, err := ec.ExecContext(ctx, args)
		done(err)
		return res, err
	}
	values, verr := namedToValues(args)
	if verr != nil {
		return nil, verr
	}
	res, err := s.base.Exec(values) //nolint:staticcheck // legacy fallback
	done(err)
	return res, err
}

func (s *wStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	done := s.g.Observe(s.query)
	if qc, ok := s.base.(driver.StmtQueryContext); ok {
		rows, err := qc.QueryContext(ctx, args)
		done(err)
		return rows, err
	}
	values, verr := namedToValues(args)
	if verr != nil {
		return nil, verr
	}
	rows, err := s.base.Query(values) //nolint:staticcheck // legacy fallback
	done(err)
	return rows, err
}

func (s *wStmt) CheckNamedValue(nv *driver.NamedValue) error {
	if ck, ok := s.base.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// ---- driver.Tx ----

type wTx struct {
	base driver.Tx
}

var _ driver.Tx = (*wTx)(nil)

func (t *wTx) Commit() error   { return t.base.Commit() }
func (t *wTx) Rollback() error { return t.base.Rollback() }

// ---- helpers ----

// namedToValues converts named values to positional values for the legacy
// Queryer/Execer/Stmt fallback paths, which predate named parameters.
func namedToValues(named []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(named))
	for i, nv := range named {
		if nv.Name != "" {
			return nil, errors.New("sqlguard: driver does not support named parameters")
		}
		values[i] = nv.Value
	}
	return values, nil
}
