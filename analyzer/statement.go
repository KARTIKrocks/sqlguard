package analyzer

// StmtKind is the top-level kind of a SQL statement.
type StmtKind int

const (
	// StmtUnknown means the parser could not determine the statement kind.
	StmtUnknown StmtKind = iota
	// StmtSelect is a SELECT (or WITH ... SELECT) query.
	StmtSelect
	// StmtInsert is an INSERT statement.
	StmtInsert
	// StmtUpdate is an UPDATE statement.
	StmtUpdate
	// StmtDelete is a DELETE statement.
	StmtDelete
	// StmtOther is a recognized statement that none of the rules target
	// (DDL, transaction control, etc.).
	StmtOther
)

// Statement is sqlguard's normalized, dialect-agnostic view of a single SQL
// statement. It carries only the semantic facts the rules need — not a full
// AST. Every Parser (the zero-dependency fallback and the optional real
// dialect parsers) populates this same struct, so rules never depend on a
// particular parser or dialect.
//
// Boolean fields are best-effort: a fallback-produced Statement may leave a
// field false when it genuinely cannot tell. Rules must treat "false" as
// "not detected", never as "proven absent", to avoid false positives.
type Statement struct {
	// Raw is the original, untouched SQL string. Reported back to users.
	Raw string

	// Kind is the statement's top-level kind.
	Kind StmtKind

	// HasWhere reports whether the statement has a WHERE clause.
	HasWhere bool

	// HasLimit reports whether the statement has a LIMIT clause.
	HasLimit bool

	// HasOrderBy reports whether the statement has an ORDER BY clause.
	HasOrderBy bool

	// HasFrom reports whether a SELECT has a FROM clause. Distinguishes
	// "SELECT * FROM t" from "SELECT 1" / "SELECT version()".
	HasFrom bool

	// SelectStar reports an unqualified "SELECT *" / "SELECT t.*" of columns.
	// It is false for aggregate forms like COUNT(*).
	SelectStar bool

	// SelectDistinct reports a select-level DISTINCT (SELECT DISTINCT ...,
	// incl. Postgres DISTINCT ON and MySQL DISTINCTROW). It is false for an
	// aggregate-level DISTINCT such as COUNT(DISTINCT col), which is unrelated.
	// The dialect parsers compute it from the AST; the fallback approximates it
	// lexically.
	SelectDistinct bool

	// InsertColumnsListed reports whether an INSERT names its target columns
	// explicitly: INSERT INTO t (a, b) VALUES (...). Only meaningful when
	// Kind == StmtInsert.
	InsertColumnsListed bool

	// LeadingWildcardLike reports a LIKE pattern beginning with a wildcard
	// (e.g. LIKE '%foo'), which prevents index use.
	LeadingWildcardLike bool

	// NonSargablePredicate reports a function or cast applied to a column on
	// the column side of a WHERE comparison (e.g. WHERE LOWER(email) = ...),
	// which prevents the use of an ordinary index on that column. Like the
	// LIKE fields, this is a literal/text-level heuristic the real parsers'
	// ASTs discard, so it is computed by the fallback lexer and preserved by
	// the dialect parsers rather than recomputed structurally.
	NonSargablePredicate bool

	// AddNotNullNoDefault reports an ALTER TABLE that adds a NOT NULL column
	// with no DEFAULT (e.g. ALTER TABLE t ADD COLUMN c int NOT NULL), which
	// fails or forces a table rewrite on a populated table. Like the other
	// text-level fields above, it is computed by the fallback lexer and
	// preserved by the dialect parsers.
	AddNotNullNoDefault bool

	// ImplicitCommaJoin reports a FROM clause that lists multiple tables
	// separated by top-level commas (FROM a, b) instead of explicit JOIN
	// syntax — the old-style join that silently produces a cartesian product
	// when its join condition is forgotten. Computed by the fallback lexer and
	// preserved (not recomputed from the AST) by the dialect parsers, so it
	// stays a best-effort heuristic even when Exact is true.
	ImplicitCommaJoin bool

	// CartesianJoin reports a multi-table FROM (comma join, CROSS JOIN, or a
	// bare JOIN) with no join condition (ON/USING/NATURAL) and no top-level
	// WHERE filter — an unconditioned cartesian product. It is the high-
	// confidence subset of ImplicitCommaJoin and also covers CROSS/bare JOIN.
	// Like ImplicitCommaJoin, it is a fallback-lexer heuristic preserved by the
	// dialect parsers, so it stays best-effort even when Exact is true.
	CartesianJoin bool

	// MaxInListLen is the largest element count among the statement's IN (...)
	// value lists (IN (SELECT ...) subqueries are excluded). It powers the
	// in-list-too-large rule's max-length threshold. Zero means no value-list
	// IN was found. Like the other counts, rules read it, never raw SQL. It is a
	// fallback-lexer heuristic preserved by the dialect parsers (the AST discards
	// the literal list it counts), so it stays best-effort even when Exact is true.
	MaxInListLen int

	// OffsetValue is the largest literal OFFSET seen (standard OFFSET n or
	// MySQL's LIMIT offset, count), powering the large-offset rule. Zero means
	// no offset, OFFSET 0, or a parameterized offset (OFFSET $1 / ?), which
	// cannot be evaluated statically and is therefore never flagged. The dialect
	// parsers read it from the AST's limit clause; the fallback scans for it.
	OffsetValue int

	// LeadingWildcardTermLen is the length of the longest searchable term
	// (the literal with surrounding % wildcards trimmed) across all
	// leading-wildcard LIKE patterns in the statement. It powers the
	// leading-wildcard rule's min-length setting. Zero means "unknown"
	// (e.g. produced by a real parser that did not compute it); rules must
	// treat zero as unknown and not as "short", to avoid false negatives.
	LeadingWildcardTermLen int

	// Exact is true when the Statement was produced by a real SQL parser
	// (structural analysis), false when produced by the regex fallback
	// (best-effort). Rules may use this to suppress lower-confidence findings.
	//
	// "Exact" covers the structural facts the dialect parsers derive from the
	// AST: Kind, HasWhere/HasLimit/HasOrderBy/HasFrom, SelectStar,
	// SelectDistinct, OffsetValue, and InsertColumnsListed. A few facts stay
	// lexical heuristics even when Exact is true — MaxInListLen,
	// ImplicitCommaJoin, CartesianJoin, and the literal/text-level fields
	// (LeadingWildcard*, NonSargablePredicate, AddNotNullNoDefault) — because
	// they read literal values the AST discards or are intentionally text-level.
	// Each such field documents this.
	Exact bool
}
