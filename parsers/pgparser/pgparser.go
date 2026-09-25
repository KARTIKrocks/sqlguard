// Package pgparser is an optional sqlguard Parser backed by a real
// PostgreSQL grammar (github.com/auxten/postgresql-parser, pure Go, no cgo).
//
// It produces exact, structural answers for the false-positive-prone facts
// (statement kind, WHERE/LIMIT/ORDER BY/FROM presence, SELECT *, explicit
// INSERT columns) instead of regex guesses. SQL the grammar rejects —
// dynamic fragments, dialect extensions, driver placeholders it can't
// handle — transparently degrades to sqlguard's zero-dependency
// FallbackParser, so analysis never breaks the caller's query path.
//
// Usage:
//
//	sqlguard.Register("sqlguard-pg", "pgx", middleware.WithParser(pgparser.New()))
//	db, _ := sql.Open("sqlguard-pg", dsn)
package pgparser

import (
	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/auxten/postgresql-parser/pkg/sql/parser"
	"github.com/auxten/postgresql-parser/pkg/sql/sem/tree"
)

// Parser implements analyzer.Parser using a PostgreSQL grammar.
type Parser struct {
	fallback analyzer.Parser
}

// New returns a Postgres-dialect Parser that falls back to the
// zero-dependency FallbackParser on parse failure.
func New() *Parser {
	return &Parser{fallback: analyzer.NewFallbackParser()}
}

var _ analyzer.Parser = (*Parser)(nil)

// Parse implements analyzer.Parser. It never returns an error: unparseable
// SQL yields the fallback parser's best-effort Statement (Exact=false).
func (p *Parser) Parse(sql string) (*analyzer.Statement, error) {
	// The fallback result is the baseline. It already detects the literal/text-
	// level fields (leading-wildcard LIKE, non-sargable predicates, unsafe
	// NOT NULL adds) that the AST loses after parsing, so we keep those fields
	// and overwrite only the structural ones.
	st, _ := p.fallback.Parse(sql)
	if st == nil {
		st = &analyzer.Statement{Raw: sql}
	}

	stmts, err := parser.Parse(sql)
	if err != nil || len(stmts) == 0 || stmts[0].AST == nil {
		//nolint:nilerr // by contract a parse failure is non-fatal: return the
		// best-effort fallback Statement (Exact=false), never an error, so a
		// grammar the parser rejects can't break the caller's query path.
		return st, nil
	}

	switch n := stmts[0].AST.(type) {
	case *tree.Select:
		resetStructural(st)
		st.Kind = analyzer.StmtSelect
		st.HasOrderBy = len(n.OrderBy) > 0
		st.HasLimit = hasRowLimit(n.Limit)
		st.OffsetValue = offsetValue(n.Limit)
		fillSelectBody(st, n.Select)
	case *tree.SelectClause:
		resetStructural(st)
		st.Kind = analyzer.StmtSelect
		fillSelectClause(st, n)
	case *tree.Delete:
		resetStructural(st)
		st.Kind = analyzer.StmtDelete
		st.HasWhere = n.Where != nil
		st.HasLimit = hasRowLimit(n.Limit)
		st.HasOrderBy = len(n.OrderBy) > 0
		st.OffsetValue = offsetValue(n.Limit)
	case *tree.Update:
		resetStructural(st)
		st.Kind = analyzer.StmtUpdate
		st.HasWhere = n.Where != nil
		st.HasLimit = hasRowLimit(n.Limit)
		st.HasOrderBy = len(n.OrderBy) > 0
		st.OffsetValue = offsetValue(n.Limit)
	case *tree.Insert:
		resetStructural(st)
		st.Kind = analyzer.StmtInsert
		st.InsertColumnsListed = len(n.Columns) > 0 || defaultValues(n)
		if !defaultValues(n) {
			// INSERT ... SELECT * copies columns by position, so a star in the
			// row source is the select-star case, not an incidental one.
			var src analyzer.Statement
			fillSelectBody(&src, n.Rows.Select)
			st.SelectStar = src.SelectStar
		}
	default:
		// A statement the grammar parsed but this parser does not model
		// (CREATE VIEW ... AS SELECT, EXPLAIN, DDL, ...). Nothing structural
		// was derived from its AST, so the fallback's facts stand and the
		// Statement is not Exact. Blanking them instead would silently drop
		// findings the default parser reports (#81).
		return st, nil
	}

	st.Exact = true
	return st, nil
}

// resetStructural clears the fields a handled AST node recomputes, so a
// fallback guess can't survive into a Statement marked Exact. Only called for
// nodes this parser models; the rest keep the fallback's values.
func resetStructural(st *analyzer.Statement) {
	st.Kind = analyzer.StmtOther
	st.HasWhere = false
	st.HasLimit = false
	st.HasOrderBy = false
	st.HasFrom = false
	st.SelectStar = false
	st.SelectDistinct = false
	st.OffsetValue = 0
	st.InsertColumnsListed = false
}

// hasRowLimit reports whether a limit clause bounds the row count. The grammar
// builds a Limit node for a bare OFFSET too, which bounds nothing (#82).
// LIMIT ALL is unbounded as well, but it is an explicit statement that no limit
// is wanted, and the fallback reads it as a LIMIT, so it counts here too.
func hasRowLimit(lim *tree.Limit) bool {
	return lim != nil && (lim.Count != nil || lim.LimitAll)
}

// defaultValues reports whether an INSERT is the "DEFAULT VALUES" form, which
// the grammar encodes as an absent row source. It names no columns and needs
// none — it inserts no data, so there is no positional column-order risk for
// insert-without-columns to warn about, matching the FallbackParser. tree's own
// Insert.DefaultValues dereferences Rows unguarded, hence the nil check here.
func defaultValues(n *tree.Insert) bool {
	return n.Rows == nil || n.Rows.Select == nil
}

// fillSelectBody unwraps the inner SelectStatement of a *tree.Select.
func fillSelectBody(st *analyzer.Statement, sel tree.SelectStatement) {
	switch c := sel.(type) {
	case *tree.SelectClause:
		fillSelectClause(st, c)
	case *tree.ParenSelect:
		if c.Select != nil {
			st.HasOrderBy = st.HasOrderBy || len(c.Select.OrderBy) > 0
			st.HasLimit = st.HasLimit || hasRowLimit(c.Select.Limit)
			if v := offsetValue(c.Select.Limit); v > st.OffsetValue {
				st.OffsetValue = v
			}
			fillSelectBody(st, c.Select.Select)
		}
	}
	// UnionClause / ValuesClause: leave structural defaults; the rules that
	// matter for those forms don't trigger on set operations.
}

// offsetValue extracts a literal OFFSET as an int, or 0 when there is no limit
// clause, no offset, or a non-literal (parameterized) offset — matching the
// large-offset rule's contract that only statically-known offsets are flagged.
func offsetValue(lim *tree.Limit) int {
	if lim == nil {
		return 0
	}
	nv, ok := lim.Offset.(*tree.NumVal)
	if !ok {
		return 0
	}
	n, err := nv.AsInt64()
	if err != nil || n < 0 {
		return 0
	}
	return int(n)
}

func fillSelectClause(st *analyzer.Statement, c *tree.SelectClause) {
	st.HasWhere = c.Where != nil
	st.HasFrom = len(c.From.Tables) > 0
	// DISTINCT and DISTINCT ON both set the select-level distinct flag; an
	// aggregate-level DISTINCT (count(DISTINCT x)) lives in the expr, not here.
	st.SelectDistinct = c.Distinct || len(c.DistinctOn) > 0
	for _, e := range c.Exprs {
		switch ex := e.Expr.(type) {
		case tree.UnqualifiedStar, *tree.UnqualifiedStar:
			st.SelectStar = true // SELECT *
		case *tree.AllColumnsSelector:
			st.SelectStar = true // SELECT t.* (resolved form)
		case *tree.UnresolvedName:
			if ex.Star { // SELECT t.* (unresolved form)
				st.SelectStar = true
			}
		}
	}
}
