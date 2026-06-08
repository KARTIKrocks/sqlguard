package analyzer

// Parser turns a raw SQL string into sqlguard's normalized Statement.
//
// Implementations:
//
//   - FallbackParser (this package): zero-dependency, best-effort, never
//     returns an error.
//   - parsers/pgparser, parsers/mysqlparser (optional modules): real
//     dialect ASTs, exact analysis, fall back to FallbackParser on parse
//     failure.
//
// A Parser used on the runtime query path MUST NOT panic and SHOULD avoid
// returning an error for SQL it merely doesn't understand — degrade to a
// best-effort Statement instead, so analysis never breaks db.Query.
type Parser interface {
	Parse(sql string) (*Statement, error)
}
