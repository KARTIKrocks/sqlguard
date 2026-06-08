package analyzer

import (
	"regexp"
	"strconv"
	"strings"
)

// FallbackParser is the zero-dependency Parser. It removes SQL comments and
// string-literal contents before pattern matching, so keywords inside
// comments or strings (and identifiers like update_at) no longer cause
// false positives. It is best-effort and never returns an error: SQL it
// cannot fully understand still yields a usable Statement with Exact=false.
type FallbackParser struct{}

// NewFallbackParser returns the default zero-dependency parser.
func NewFallbackParser() *FallbackParser { return &FallbackParser{} }

var (
	// I?LIKE matches both LIKE and Postgres' case-insensitive ILIKE; the \b
	// before it keeps the "I" from matching inside words like DISLIKE.
	fbLeadingWildcardRe = regexp.MustCompile(`(?i)\bI?LIKE\s+['"]\s*%`)
	// Best-effort capture of a LIKE/ILIKE pattern's literal body. Does not model
	// embedded/escaped quotes; the fallback is heuristic by contract.
	fbLikeLiteralRe = regexp.MustCompile(`(?i)\bI?LIKE\s+['"]([^'"]*)['"]`)
	fbWhereRe       = regexp.MustCompile(`(?i)\bWHERE\b`)
	fbLimitRe       = regexp.MustCompile(`(?i)\bLIMIT\b`)
	fbOrderByRe     = regexp.MustCompile(`(?i)\bORDER\s+BY\b`)
	fbFromRe        = regexp.MustCompile(`(?i)\bFROM\b`)
	fbSelectStarRe  = regexp.MustCompile(`(?i)\bSELECT\s+(?:DISTINCT\s+)?(?:[a-z_][a-z0-9_]*\s*\.\s*)?\*`)
	// fbSelectDistinctRe anchors DISTINCT to right after SELECT, so an
	// aggregate-level DISTINCT (COUNT(DISTINCT x)) does not match.
	fbSelectDistinctRe = regexp.MustCompile(`(?i)\bSELECT\s+DISTINCT(?:ROW)?\b`)
	fbIntoRe           = regexp.MustCompile(`(?i)\bINTO\b`)
	// fbInsertDataRe marks the start of an INSERT's data clause, after the
	// target table (and its optional column list). VALUES? covers MySQL's
	// singular VALUE; SELECT/WITH/TABLE cover INSERT ... SELECT and friends.
	fbInsertDataRe = regexp.MustCompile(`(?i)\b(VALUES?|SELECT|WITH|TABLE|SET|DEFAULT)\b`)
	fbLeadKindRe   = regexp.MustCompile(`(?i)^\s*\(*\s*(SELECT|INSERT|UPDATE|DELETE|WITH)\b`)
	fbDMLWordRe    = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE)\b`)

	// fbWhereRegionEndRe marks the first clause keyword that ends the WHERE
	// region, so a function in ORDER BY / GROUP BY / HAVING isn't read as a
	// WHERE predicate.
	fbWhereRegionEndRe = regexp.MustCompile(`(?i)\b(GROUP\s+BY|ORDER\s+BY|HAVING|LIMIT|OFFSET|WINDOW|FETCH|FOR\s+UPDATE)\b`)
	// fbFuncOnColumnRe matches IDENT(args)<op>: a function/cast call whose
	// closing paren is immediately followed by a comparison operator. The
	// operator-after-paren shape is what restricts it to the column side of a
	// predicate (WHERE LOWER(c) = ...), not the value side (WHERE c = ABS(x)).
	fbFuncOnColumnRe = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s*\(([^()]*)\)\s*(?:=|<>|!=|<=|>=|<|>|\bLIKE\b|\bIN\b|\bBETWEEN\b)`)
	// fbArgIdentRe checks that a function's arguments contain a column-like
	// identifier, so NOW() and LOWER('x') (literal blanked to '') are skipped.
	fbArgIdentRe = regexp.MustCompile(`[a-zA-Z_]`)

	fbAlterTableRe = regexp.MustCompile(`(?i)^\s*ALTER\s+TABLE\b`)
	// fbAddActionRe matches the start of an ALTER action and captures the
	// first token after ADD [COLUMN] — a column name for a column add, or a
	// keyword (CONSTRAINT, CHECK, ...) for the forms we must skip.
	fbAddActionRe = regexp.MustCompile(`(?i)\bADD\s+(?:COLUMN\s+)?(\w+)`)
	fbNotNullRe   = regexp.MustCompile(`(?i)\bNOT\s+NULL\b`)
	fbDefaultRe   = regexp.MustCompile(`(?i)\bDEFAULT\b`)

	// fbFromRegionEndRe marks the first clause keyword that ends the FROM
	// region, so commas after it (an IN list, GROUP BY, etc.) aren't read as
	// join separators.
	fbFromRegionEndRe = regexp.MustCompile(`(?i)\b(WHERE|GROUP\s+BY|ORDER\s+BY|HAVING|LIMIT|OFFSET|WINDOW|FETCH|FOR|UNION|EXCEPT|INTERSECT)\b`)
	fbJoinRe          = regexp.MustCompile(`(?i)\bJOIN\b`)
	// fbJoinCondRe matches anything that conditions a join — an ON/USING
	// predicate or a NATURAL join (which joins on common columns) — so a join
	// carrying one of these is not treated as a cartesian product.
	fbJoinCondRe = regexp.MustCompile(`(?i)\b(ON|USING|NATURAL)\b`)
	// fbInListRe matches the opening of an IN value list (NOT IN matches too).
	fbInListRe = regexp.MustCompile(`(?i)\bIN\s*\(`)
	// fbSubqueryStartRe recognizes an IN (...) body that is a subquery (or set)
	// rather than a value list, so it is not counted.
	fbSubqueryStartRe = regexp.MustCompile(`(?i)^\(*\s*(SELECT|WITH|VALUES|TABLE)\b`)
	// fbOffsetRe captures a literal standard OFFSET n (incl. OFFSET n ROWS).
	fbOffsetRe = regexp.MustCompile(`(?i)\bOFFSET\s+(\d+)`)
	// fbLimitOffsetRe captures the offset of MySQL's LIMIT offset, count form.
	fbLimitOffsetRe = regexp.MustCompile(`(?i)\bLIMIT\s+(\d+)\s*,\s*\d+`)
)

// fbNonSargableSkipFuncs are tokens that can appear as IDENT before "(" but
// are SQL keywords, not functions wrapping a column.
var fbNonSargableSkipFuncs = map[string]bool{
	"in": true, "exists": true, "any": true, "all": true,
	"some": true, "and": true, "or": true, "not": true,
}

// fbAddNonColumnKeywords are the tokens following ADD that mean the action is
// not a column add (so a stray NOT NULL, e.g. inside a CHECK constraint, isn't
// mistaken for a NOT NULL column).
var fbAddNonColumnKeywords = map[string]bool{
	"constraint": true, "primary": true, "foreign": true,
	"unique": true, "check": true, "key": true, "index": true,
}

// Parse implements Parser. It always returns a non-nil Statement and a nil
// error.
func (p *FallbackParser) Parse(sql string) (*Statement, error) {
	st := &Statement{Raw: sql, Exact: false}

	noComments := stripComments(sql)

	// Leading-wildcard LIKE is detected before literal contents are blanked,
	// because the pattern lives inside the literal. Comments are already gone,
	// so a commented-out LIKE won't trigger.
	st.LeadingWildcardLike = fbLeadingWildcardRe.MatchString(noComments)
	if st.LeadingWildcardLike {
		st.LeadingWildcardTermLen = leadingWildcardTermLen(noComments)
	}

	sanitized := blankStringLiterals(noComments)

	st.Kind = detectKind(sanitized)
	st.HasWhere = fbWhereRe.MatchString(sanitized)
	st.HasLimit = fbLimitRe.MatchString(sanitized)
	st.HasOrderBy = hasTopLevelOrderBy(sanitized)
	st.HasFrom = fbFromRe.MatchString(sanitized)
	st.SelectStar = fbSelectStarRe.MatchString(sanitized)
	st.SelectDistinct = fbSelectDistinctRe.MatchString(sanitized)
	st.NonSargablePredicate = hasNonSargablePredicate(sanitized)
	st.AddNotNullNoDefault = hasUnsafeAddNotNull(sanitized)
	st.ImplicitCommaJoin = hasImplicitCommaJoin(sanitized)
	st.CartesianJoin = hasCartesianJoin(sanitized)
	st.MaxInListLen = maxInListLen(sanitized)
	st.OffsetValue = maxOffset(sanitized)

	if st.Kind == StmtInsert {
		st.InsertColumnsListed = insertColumnsListed(sanitized)
	}

	return st, nil
}

// insertColumnsListed reports whether an INSERT names its target columns
// explicitly. It inspects the span between INTO and the data clause
// (VALUES / SELECT / WITH / TABLE): an explicit column list shows up there as a
// "(". The "VALUES"-only shape the old regex matched missed INSERT ... SELECT
// (and CTE-prefixed inserts), which carry the same schema-change risk. MySQL's
// "SET col = ..." names its columns, and "DEFAULT VALUES" inserts no data, so
// both count as listed (no positional column-order risk to warn about).
// Comment-free, literal-blanked input expected; heuristic by contract.
func insertColumnsListed(sanitized string) bool {
	loc := fbIntoRe.FindStringIndex(sanitized)
	if loc == nil {
		return true // no INTO found — can't tell, don't flag
	}
	rest := sanitized[loc[1]:]
	data := fbInsertDataRe.FindStringIndex(rest)
	if data == nil {
		return true // no recognizable data clause — don't flag
	}
	switch strings.ToUpper(strings.TrimSpace(rest[data[0]:data[1]])) {
	case "SET", "DEFAULT":
		return true
	}
	// Columns are listed iff a "(" appears between the table name and the data
	// clause. A bare table reference (incl. schema.table) has no parens there.
	return strings.Contains(rest[:data[0]], "(")
}

// leadingWildcardTermLen returns the length of the longest searchable term
// (the LIKE literal with surrounding '%' trimmed) among patterns that begin
// with a wildcard. Comment-free input is expected.
func leadingWildcardTermLen(noComments string) int {
	max := 0
	for _, m := range fbLikeLiteralRe.FindAllStringSubmatch(noComments, -1) {
		body := strings.TrimSpace(m[1])
		if !strings.HasPrefix(body, "%") {
			continue
		}
		if n := len(strings.Trim(body, "%")); n > max {
			max = n
		}
	}
	return max
}

// hasNonSargablePredicate reports whether the WHERE clause applies a function
// or cast to a column (WHERE LOWER(email) = ...), which defeats an index on
// that column. Input must be comment-free and have its string literals
// blanked. Scope is limited to the WHERE region so functions in the SELECT
// list, ORDER BY, or GROUP BY don't false-fire.
func hasNonSargablePredicate(sanitized string) bool {
	region := whereRegion(sanitized)
	if region == "" {
		return false
	}
	for _, m := range fbFuncOnColumnRe.FindAllStringSubmatch(region, -1) {
		if fbNonSargableSkipFuncs[strings.ToLower(m[1])] {
			continue // a keyword like IN(...) / EXISTS(...), not a function
		}
		if !fbArgIdentRe.MatchString(m[2]) {
			continue // no column in the args (e.g. NOW(), LOWER('x'))
		}
		return true
	}
	return false
}

// whereRegion returns the slice of sanitized SQL from the WHERE keyword up to
// the next clause keyword (ORDER BY, GROUP BY, HAVING, LIMIT, ...), or "" when
// there is no WHERE clause.
func whereRegion(sanitized string) string {
	loc := fbWhereRe.FindStringIndex(sanitized)
	if loc == nil {
		return ""
	}
	region := sanitized[loc[1]:]
	// End the region at the first clause keyword that sits at the WHERE's own
	// nesting level. A keyword inside a subquery in the WHERE (e.g. ORDER BY /
	// LIMIT in "WHERE id IN (SELECT ... ORDER BY x LIMIT 1)") is at depth > 0
	// and must not cut the region short, which would drop predicates after it.
	for _, end := range fbWhereRegionEndRe.FindAllStringIndex(region, -1) {
		if parenDepthBefore(region, end[0]) == 0 {
			return region[:end[0]]
		}
	}
	return region
}

// hasUnsafeAddNotNull reports whether an ALTER TABLE adds a NOT NULL column
// without a DEFAULT — which errors or rewrites the table on a populated table.
// Input must be comment-free with string literals blanked. The statement is
// split on top-level commas so each ADD action is judged independently (and a
// numeric type's own comma, e.g. NUMERIC(10,2), isn't a split point).
func hasUnsafeAddNotNull(sanitized string) bool {
	if !fbAlterTableRe.MatchString(sanitized) {
		return false
	}
	for _, seg := range splitTopLevelCommas(sanitized) {
		m := fbAddActionRe.FindStringSubmatch(seg)
		if m == nil {
			continue // not an ADD action
		}
		if fbAddNonColumnKeywords[strings.ToLower(m[1])] {
			continue // ADD CONSTRAINT / CHECK / KEY / ... — not a column add
		}
		if fbNotNullRe.MatchString(seg) && !fbDefaultRe.MatchString(seg) {
			return true
		}
	}
	return false
}

// splitTopLevelCommas splits s on commas that are not nested inside
// parentheses. Used to separate the actions of a multi-action ALTER TABLE
// without breaking parenthesized type specs or expressions.
func splitTopLevelCommas(s string) []string {
	var segs []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				segs = append(segs, s[start:i])
				start = i + 1
			}
		}
	}
	return append(segs, s[start:])
}

// hasImplicitCommaJoin reports whether the FROM clause lists multiple tables
// separated by top-level commas (FROM a, b) rather than explicit JOIN syntax.
// Input must be comment-free with string literals blanked. The FROM region is
// found and bounded paren-aware so that a FROM inside EXTRACT(... FROM ...) or
// a subquery, and commas inside function calls / subqueries / IN lists, are
// not mistaken for join separators.
func hasImplicitCommaJoin(sanitized string) bool {
	region := fromRegion(sanitized)
	if region == "" {
		return false
	}
	return len(splitTopLevelCommas(region)) > 1
}

// hasCartesianJoin reports whether the FROM clause joins multiple tables (via
// a top-level comma, CROSS JOIN, or a bare JOIN) with no join condition
// (ON/USING/NATURAL) anywhere in the FROM region and no top-level WHERE — an
// unconditioned cartesian product. It is deliberately conservative: if any
// join condition or WHERE filter is present it does not fire, so mixed queries
// (some joins conditioned) yield a false negative rather than a false positive.
// Input must be comment-free with string literals blanked.
func hasCartesianJoin(sanitized string) bool {
	region := fromRegion(sanitized)
	if region == "" {
		return false
	}
	multiTable := len(splitTopLevelCommas(region)) > 1 || hasTopLevelJoin(region)
	if !multiTable {
		return false
	}
	if fbJoinCondRe.MatchString(region) || hasTopLevelWhere(sanitized) {
		return false
	}
	return true
}

// hasTopLevelWhere reports whether a WHERE keyword appears at parenthesis depth
// zero (a WHERE inside a subquery does not filter an outer cartesian product).
func hasTopLevelWhere(sanitized string) bool {
	for _, loc := range fbWhereRe.FindAllStringIndex(sanitized, -1) {
		if parenDepthBefore(sanitized, loc[0]) == 0 {
			return true
		}
	}
	return false
}

// hasTopLevelJoin reports whether a JOIN keyword appears at parenthesis depth
// zero within the FROM region — an outer-level join (incl. CROSS/bare JOIN),
// not one inside a subquery. region is a depth-zero slice from fromRegion, so
// depth is measured relative to it. This is the JOIN counterpart of the
// paren-aware comma split: without it a JOIN in a FROM-clause subquery would be
// read as an outer cartesian product.
func hasTopLevelJoin(region string) bool {
	for _, loc := range fbJoinRe.FindAllStringIndex(region, -1) {
		if parenDepthBefore(region, loc[0]) == 0 {
			return true
		}
	}
	return false
}

// hasTopLevelOrderBy reports whether an ORDER BY appears at parenthesis depth
// zero — a result-set sort, not a window-function (OVER (ORDER BY ...)),
// ordered-aggregate (GROUP_CONCAT(... ORDER BY ...), WITHIN GROUP (ORDER BY
// ...)), or subquery ordering, none of which sort the statement's result set.
func hasTopLevelOrderBy(sanitized string) bool {
	for _, loc := range fbOrderByRe.FindAllStringIndex(sanitized, -1) {
		if parenDepthBefore(sanitized, loc[0]) == 0 {
			return true
		}
	}
	return false
}

// maxInListLen returns the largest element count among the statement's
// IN (...) value lists. IN (SELECT ...) / IN (VALUES ...) subqueries are not
// counted. Input must be comment-free with string literals blanked — commas
// between blanked literals survive, so element counting is unaffected.
func maxInListLen(sanitized string) int {
	max := 0
	for _, loc := range fbInListRe.FindAllStringIndex(sanitized, -1) {
		inner, ok := parenContent(sanitized, loc[1]-1) // loc[1]-1 is the "("
		if !ok {
			continue
		}
		if strings.TrimSpace(inner) == "" || fbSubqueryStartRe.MatchString(strings.TrimSpace(inner)) {
			continue
		}
		if n := len(splitTopLevelCommas(inner)); n > max {
			max = n
		}
	}
	return max
}

// maxOffset returns the largest literal offset in the statement, considering
// both the standard OFFSET n form and MySQL's LIMIT offset, count form. A
// parameterized offset (OFFSET $1 / ?) matches neither and yields 0. Input
// must be comment-free with string literals blanked (numeric literals, which
// is what an offset is, are left intact). An offset literal too large for int
// is ignored (treated as no offset) — a rare, harmless false negative.
func maxOffset(sanitized string) int {
	max := 0
	consider := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(sanitized, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	consider(fbOffsetRe)
	consider(fbLimitOffsetRe)
	return max
}

// parenContent returns the substring between the parenthesis at index open and
// its matching close paren (exclusive of both), and true, or "" and false if
// unbalanced.
func parenContent(s string, open int) (string, bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

// fromRegion returns the slice of sanitized SQL between the first top-level
// FROM keyword and the next top-level clause keyword (WHERE, GROUP BY, a set
// operator, ...), or "" when there is no top-level FROM. "Top-level" means at
// parenthesis depth zero, so subquery and function-argument keywords are
// ignored.
func fromRegion(sanitized string) string {
	fromEnd := -1
	for _, loc := range fbFromRe.FindAllStringIndex(sanitized, -1) {
		if parenDepthBefore(sanitized, loc[0]) == 0 {
			fromEnd = loc[1]
			break
		}
	}
	if fromEnd == -1 {
		return ""
	}
	region := sanitized[fromEnd:]
	for _, loc := range fbFromRegionEndRe.FindAllStringIndex(region, -1) {
		if parenDepthBefore(region, loc[0]) == 0 {
			return region[:loc[0]]
		}
	}
	return region
}

// parenDepthBefore returns the net parenthesis nesting depth at index idx
// (count of unmatched '(' in s[:idx]).
func parenDepthBefore(s string, idx int) int {
	depth := 0
	for i := range idx {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth
}

func detectKind(sanitized string) StmtKind {
	m := fbLeadKindRe.FindStringSubmatch(sanitized)
	if m == nil {
		return StmtOther
	}
	switch strings.ToUpper(m[1]) {
	case "SELECT":
		return StmtSelect
	case "INSERT":
		return StmtInsert
	case "UPDATE":
		return StmtUpdate
	case "DELETE":
		return StmtDelete
	case "WITH":
		// A CTE feeds a main statement. Best-effort: if an INSERT/UPDATE/DELETE
		// keyword appears anywhere, treat it as that; otherwise a SELECT.
		if w := fbDMLWordRe.FindString(sanitized); w != "" {
			switch strings.ToUpper(w) {
			case "INSERT":
				return StmtInsert
			case "UPDATE":
				return StmtUpdate
			case "DELETE":
				return StmtDelete
			}
		}
		return StmtSelect
	}
	return StmtOther
}

// stripComments removes -- line comments and /* */ block comments, replacing
// each with a single space so token boundaries are preserved. It does not
// remove comment markers that appear inside string literals.
func stripComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\'' || c == '"':
			i = copyStringLiteral(&b, s, i)
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i = skipLineComment(s, i)
			b.WriteByte(' ')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlockComment(s, i)
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// copyStringLiteral writes the string literal that begins at s[i] (a quote
// byte) verbatim, treating a doubled quote (two of the same quote byte in a
// row) as an escaped quote rather than the terminator, and returns the index
// just past the literal.
func copyStringLiteral(b *strings.Builder, s string, i int) int {
	q := s[i]
	b.WriteByte(q)
	i++
	for i < len(s) {
		b.WriteByte(s[i])
		if s[i] == q {
			if i+1 < len(s) && s[i+1] == q { // doubled-quote escape
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// skipLineComment returns the index of the newline (or end of input) that
// terminates the -- comment starting at i.
func skipLineComment(s string, i int) int {
	for i < len(s) && s[i] != '\n' {
		i++
	}
	return i
}

// skipBlockComment returns the index just past the */ that closes the block
// comment starting at i (or end of input if unterminated).
func skipBlockComment(s string, i int) int {
	i += 2
	for i+1 < len(s) && (s[i] != '*' || s[i+1] != '/') {
		i++
	}
	return i + 2
}

// blankStringLiterals replaces the contents of every string literal with an
// empty literal, so SQL keywords that appear inside string values cannot be
// mistaken for clauses. Input must already be comment-free.
func blankStringLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\'' || c == '"' {
			q := c
			b.WriteByte(q)
			i++
			for i < len(s) {
				if s[i] == q {
					if i+1 < len(s) && s[i+1] == q { // doubled-quote escape
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			b.WriteByte(q)
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}
