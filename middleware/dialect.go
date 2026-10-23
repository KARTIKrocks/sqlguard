package middleware

import (
	"reflect"
	"strings"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// WithDialect records the SQL dialect of the analyzed queries, a hint for how
// the redaction lexer should read quotes and escapes; redaction output does
// not use it yet. It outranks a `dialect:` in the config file and anything
// detected.
func WithDialect(d analyzer.Dialect) Option {
	return func(o *options) {
		o.dialect = d
		o.dialectSet = true
	}
}

// WithDetectedDialect sets the dialect an integration knows from the driver or
// ORM it wraps. It applies only when neither WithDialect, the config file nor
// a single-dialect parser names one. DialectUnknown is ignored, so a failed
// detection never clears a successful one.
func WithDetectedDialect(d analyzer.Dialect) Option {
	return func(o *options) {
		if d != analyzer.DialectUnknown {
			o.detectedDialect = d
		}
	}
}

// resolveDialect applies the precedence: WithDialect, then the analyzer's own
// (config file, then parser), then the detected one.
func resolveDialect(o *options) {
	switch {
	case o.dialectSet:
		o.analyzer = o.analyzer.WithDialect(o.dialect)
	case o.analyzer.Dialect() == analyzer.DialectUnknown && o.detectedDialect != analyzer.DialectUnknown:
		o.analyzer = o.analyzer.WithDialect(o.detectedDialect)
	}
}

// driverDialects maps a database/sql driver's package path to its dialect.
var driverDialects = []struct {
	pkg     string
	dialect analyzer.Dialect
}{
	{"github.com/jackc/pgx", analyzer.DialectPostgres},
	{"github.com/lib/pq", analyzer.DialectPostgres},
	{"github.com/go-sql-driver/mysql", analyzer.DialectMySQL},
}

// driverDialect detects the dialect from the base driver's package. A driver
// hidden behind another wrapper, or one not listed, stays unknown.
func driverDialect(d any) analyzer.Dialect {
	t := reflect.TypeOf(d)
	if t == nil {
		return analyzer.DialectUnknown
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	pkg := t.PkgPath()
	for _, m := range driverDialects {
		if pkg == m.pkg || strings.HasPrefix(pkg, m.pkg+"/") {
			return m.dialect
		}
	}
	return analyzer.DialectUnknown
}
