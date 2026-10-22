package analyzer

import "strings"

// Dialect is the SQL dialect an Analyzer's queries are written in. It is a
// hint for the literal lexer behind Redact and Fingerprint: where dialects
// disagree about quoting, an unknown dialect has to hedge, and a known one
// need not. DialectUnknown is the zero value and keeps the dialect-agnostic
// behaviour.
type Dialect uint8

// The dialects an Analyzer can be told about.
const (
	DialectUnknown Dialect = iota
	DialectPostgres
	DialectMySQL
)

// String returns the dialect's config name: "postgres", "mysql" or "unknown".
func (d Dialect) String() string {
	switch d {
	case DialectPostgres:
		return "postgres"
	case DialectMySQL:
		return "mysql"
	default:
		return "unknown"
	}
}

// ParseDialect maps a dialect or driver name to a Dialect, case-insensitively.
// It accepts the config names and the names database/sql drivers and ORMs
// report ("pgx", "postgresql", "mariadb", …), and returns false for anything
// else, including "unknown".
func ParseDialect(name string) (Dialect, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "postgres", "postgresql", "pg", "pgx":
		return DialectPostgres, true
	case "mysql", "mariadb":
		return DialectMySQL, true
	default:
		return DialectUnknown, false
	}
}

// dialectParser is implemented by a Parser that parses one dialect only, so
// WithParser can take the dialect from it.
type dialectParser interface {
	Dialect() Dialect
}
