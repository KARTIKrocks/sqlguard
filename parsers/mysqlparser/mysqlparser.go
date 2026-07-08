// Package mysqlparser is an optional sqlguard Parser backed by a real
// MySQL grammar (github.com/xwb1989/sqlparser — a pure-Go, no-cgo,
// lightweight Vitess-derived MySQL parser).
//
// It produces exact, structural answers for the false-positive-prone facts
// (statement kind, WHERE/LIMIT/ORDER BY/FROM presence, SELECT *, explicit
// INSERT columns) instead of regex guesses. SQL the grammar rejects —
// CTEs it doesn't support, dynamic fragments, dialect extensions —
// transparently degrades to sqlguard's zero-dependency FallbackParser, so
// analysis never breaks the caller's query path.
//
// Usage:
//
//	sqlguard.Register("sqlguard-mysql", "mysql", middleware.WithParser(mysqlparser.New()))
//	db, _ := sql.Open("sqlguard-mysql", dsn)
package mysqlparser

import (
	"strconv"
	"strings"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/xwb1989/sqlparser"
)

// Parser implements analyzer.Parser using a MySQL grammar.
type Parser struct {
	fallback analyzer.Parser
}

// New returns a MySQL-dialect Parser that falls back to the
// zero-dependency FallbackParser on parse failure.
func New() *Parser {
	return &Parser{fallback: analyzer.NewFallbackParser()}
}

var _ analyzer.Parser = (*Parser)(nil)

// Parse implements analyzer.Parser. It never returns an error: unparseable
// SQL yields the fallback parser's best-effort Statement (Exact=false).
func (p *Parser) Parse(sql string) (*analyzer.Statement, error) {
	// Baseline from the fallback. It detects the literal/text-level fields
	// (leading-wildcard LIKE, non-sargable predicates, unsafe NOT NULL adds)
	// that the AST loses after parsing, so those fields are kept; only
	// structural fields are overwritten.
	st, _ := p.fallback.Parse(sql)
	if st == nil {
		st = &analyzer.Statement{Raw: sql}
	}

	ast, err := sqlparser.Parse(sql)
	if err != nil || ast == nil {
		//nolint:nilerr // by contract a parse failure is non-fatal: return the
		// best-effort fallback Statement (Exact=false), never an error, so a
		// grammar the parser rejects can't break the caller's query path.
		return st, nil
	}

	st.Kind = analyzer.StmtOther
	st.HasWhere = false
	st.HasLimit = false
	st.HasOrderBy = false
	st.HasFrom = false
	st.SelectStar = false
	st.SelectDistinct = false
	st.OffsetValue = 0
	st.InsertColumnsListed = false

	switch n := ast.(type) {
	case *sqlparser.Select:
		st.Kind = analyzer.StmtSelect
		st.HasWhere = n.Where != nil
		st.HasLimit = n.Limit != nil
		st.HasOrderBy = len(n.OrderBy) > 0
		st.HasFrom = hasRealFrom(n.From)
		st.SelectDistinct = n.Distinct != ""
		st.OffsetValue = offsetValue(n.Limit)
		for _, e := range n.SelectExprs {
			if _, ok := e.(*sqlparser.StarExpr); ok { // '*' or 'table.*'
				st.SelectStar = true
			}
		}
	case *sqlparser.Delete:
		st.Kind = analyzer.StmtDelete
		st.HasWhere = n.Where != nil
		st.HasLimit = n.Limit != nil
		st.HasOrderBy = len(n.OrderBy) > 0
		st.OffsetValue = offsetValue(n.Limit)
	case *sqlparser.Update:
		st.Kind = analyzer.StmtUpdate
		st.HasWhere = n.Where != nil
		st.HasLimit = n.Limit != nil
		st.HasOrderBy = len(n.OrderBy) > 0
		st.OffsetValue = offsetValue(n.Limit)
	case *sqlparser.Insert:
		st.Kind = analyzer.StmtInsert
		st.InsertColumnsListed = len(n.Columns) > 0
	}

	st.Exact = true
	return st, nil
}

// offsetValue extracts a literal OFFSET as an int, or 0 when there is no limit
// clause, no offset, or a non-literal (parameterized) offset — matching the
// large-offset rule's contract that only statically-known offsets are flagged.
// Covers both "LIMIT count OFFSET n" and MySQL's "LIMIT n, count" (the parser
// puts n in Offset for both).
func offsetValue(lim *sqlparser.Limit) int {
	if lim == nil || lim.Offset == nil {
		return 0
	}
	v, ok := lim.Offset.(*sqlparser.SQLVal)
	if !ok || v.Type != sqlparser.IntVal {
		return 0
	}
	n, err := strconv.Atoi(string(v.Val))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// hasRealFrom reports whether a FROM clause references a real table, not the
// implicit "dual" the parser injects for FROM-less selects like SELECT 1.
func hasRealFrom(from sqlparser.TableExprs) bool {
	for _, te := range from {
		ate, ok := te.(*sqlparser.AliasedTableExpr)
		if !ok {
			return true // join / subquery / etc. — a real source
		}
		if tn, ok := ate.Expr.(sqlparser.TableName); ok {
			// Case-insensitive: sqlparser preserves the casing of backticked
			// identifiers, so `DUAL` would otherwise read as a real table.
			if strings.EqualFold(tn.Name.String(), "dual") {
				continue
			}
		}
		return true
	}
	return false
}
