package analyzer

import "fmt"

// Built-in rules self-register so they are addressable by name for config
// (enable/disable, severity overrides, settings) and suppressions. Adding a
// new rule is just another Register call here — no other plumbing changes.
func init() {
	Register(RuleSpec{Name: "select-star", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckSelectStar }})
	Register(RuleSpec{Name: "leading-wildcard", DefaultSeverity: SeverityWarning,
		Factory: func(s Settings) Rule { return leadingWildcardRule(s.Int("min-length", 0)) }})
	Register(RuleSpec{Name: "delete-without-where", DefaultSeverity: SeverityCritical,
		Factory: func(Settings) Rule { return CheckDeleteWithoutWhere }})
	Register(RuleSpec{Name: "update-without-where", DefaultSeverity: SeverityCritical,
		Factory: func(Settings) Rule { return CheckUpdateWithoutWhere }})
	Register(RuleSpec{Name: "insert-without-columns", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckInsertWithoutColumns }})
	Register(RuleSpec{Name: "select-without-limit", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckSelectWithoutLimit }})
	Register(RuleSpec{Name: "orderby-without-limit", DefaultSeverity: SeverityInfo,
		Factory: func(Settings) Rule { return CheckOrderByWithoutLimit }})
	Register(RuleSpec{Name: "non-sargable-predicate", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckNonSargablePredicate }})
	Register(RuleSpec{Name: "add-not-null-without-default", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckAddNotNullWithoutDefault }})
	Register(RuleSpec{Name: "implicit-join", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckImplicitJoin }})
	Register(RuleSpec{Name: "cartesian-join", DefaultSeverity: SeverityWarning,
		Factory: func(Settings) Rule { return CheckCartesianJoin }})
	Register(RuleSpec{Name: "in-list-too-large", DefaultSeverity: SeverityWarning,
		Factory: func(s Settings) Rule { return inListRule(s.Int("max-length", 100)) }})
	Register(RuleSpec{Name: "large-offset", DefaultSeverity: SeverityWarning,
		Factory: func(s Settings) Rule { return largeOffsetRule(s.Int("threshold", 1000)) }})
	Register(RuleSpec{Name: "select-distinct", DefaultSeverity: SeverityInfo,
		Factory: func(Settings) Rule { return CheckSelectDistinct }})
}

// CheckSelectStar detects SELECT * usage.
func CheckSelectStar(s *Statement) (Result, bool) {
	if s.SelectStar {
		return Result{
			RuleName:   "select-star",
			Query:      s.Raw,
			Message:    "SELECT * detected. Selecting all columns can hurt performance.",
			Suggestion: "Select only the columns you need.",
		}, true
	}
	return Result{}, false
}

// CheckLeadingWildcard detects LIKE patterns with leading wildcards, using
// the rule's default settings (no minimum term length).
func CheckLeadingWildcard(s *Statement) (Result, bool) {
	return leadingWildcardRule(0)(s)
}

// leadingWildcardRule builds the leading-wildcard rule. When minLen > 0, a
// leading-wildcard LIKE is flagged only if its searchable term is at least
// minLen characters long, so short patterns like LIKE '%x%' can be tolerated.
// A statement whose term length is unknown (0, e.g. produced by a real
// parser that did not compute it) is still flagged, to avoid false negatives.
func leadingWildcardRule(minLen int) Rule {
	return func(s *Statement) (Result, bool) {
		if !s.LeadingWildcardLike {
			return Result{}, false
		}
		if minLen > 0 && s.LeadingWildcardTermLen > 0 && s.LeadingWildcardTermLen < minLen {
			return Result{}, false
		}
		return Result{
			RuleName:   "leading-wildcard",
			Query:      s.Raw,
			Message:    "LIKE with leading wildcard detected. Index cannot be used.",
			Suggestion: "Use prefix search or a full-text index.",
		}, true
	}
}

// CheckDeleteWithoutWhere detects DELETE statements without a WHERE clause.
func CheckDeleteWithoutWhere(s *Statement) (Result, bool) {
	if s.Kind == StmtDelete && !s.HasWhere {
		return Result{
			RuleName:   "delete-without-where",
			Query:      s.Raw,
			Message:    "DELETE without WHERE clause detected. This will delete all rows.",
			Suggestion: "Add a WHERE clause to limit the scope of the delete.",
		}, true
	}
	return Result{}, false
}

// CheckUpdateWithoutWhere detects UPDATE statements without a WHERE clause.
func CheckUpdateWithoutWhere(s *Statement) (Result, bool) {
	if s.Kind == StmtUpdate && !s.HasWhere {
		return Result{
			RuleName:   "update-without-where",
			Query:      s.Raw,
			Message:    "UPDATE without WHERE clause detected. This will update all rows.",
			Suggestion: "Add a WHERE clause to limit the scope of the update.",
		}, true
	}
	return Result{}, false
}

// CheckInsertWithoutColumns detects INSERT statements without an explicit
// column list.
func CheckInsertWithoutColumns(s *Statement) (Result, bool) {
	if s.Kind == StmtInsert && !s.InsertColumnsListed {
		return Result{
			RuleName:   "insert-without-columns",
			Query:      s.Raw,
			Message:    "INSERT without explicit column list. This breaks if table schema changes.",
			Suggestion: "Specify columns explicitly: INSERT INTO table (col1, col2) VALUES (...).",
		}, true
	}
	return Result{}, false
}

// CheckSelectWithoutLimit detects SELECT statements without a LIMIT clause.
// Only flags queries that have a FROM clause (to skip SELECT 1, SELECT
// version(), etc.) and don't have WHERE, to reduce noise.
func CheckSelectWithoutLimit(s *Statement) (Result, bool) {
	if s.Kind == StmtSelect && s.HasFrom && !s.HasLimit && !s.HasWhere {
		return Result{
			RuleName:   "select-without-limit",
			Query:      s.Raw,
			Message:    "SELECT without LIMIT or WHERE clause. May return excessive rows.",
			Suggestion: "Add a LIMIT clause or WHERE filter to restrict results.",
		}, true
	}
	return Result{}, false
}

// CheckNonSargablePredicate detects a function or cast applied to a column on
// the column side of a WHERE comparison (e.g. WHERE LOWER(email) = ...), which
// prevents an ordinary index on that column from being used.
func CheckNonSargablePredicate(s *Statement) (Result, bool) {
	if s.NonSargablePredicate {
		return Result{
			RuleName:   "non-sargable-predicate",
			Query:      s.Raw,
			Message:    "Function applied to a column in WHERE prevents index use.",
			Suggestion: "Compare the bare column instead, or add a matching expression/function index.",
		}, true
	}
	return Result{}, false
}

// CheckAddNotNullWithoutDefault detects an ALTER TABLE that adds a NOT NULL
// column with no DEFAULT, which errors or forces a full table rewrite on a
// populated table.
func CheckAddNotNullWithoutDefault(s *Statement) (Result, bool) {
	if s.AddNotNullNoDefault {
		return Result{
			RuleName:   "add-not-null-without-default",
			Query:      s.Raw,
			Message:    "ADD COLUMN ... NOT NULL without DEFAULT fails or rewrites the table on a populated table.",
			Suggestion: "Add a DEFAULT, or split into: add the column nullable, backfill, then SET NOT NULL.",
		}, true
	}
	return Result{}, false
}

// CheckInListTooLarge detects an IN (...) value list with more elements than
// the default threshold (100). Use the registry / config to tune max-length.
func CheckInListTooLarge(s *Statement) (Result, bool) {
	return inListRule(100)(s)
}

// inListRule builds the in-list-too-large rule. It flags a statement whose
// largest IN (...) value list has more than maxLen elements. A maxLen of 0
// flags any value-list IN; subquery INs are never counted (MaxInListLen
// excludes them).
func inListRule(maxLen int) Rule {
	return func(s *Statement) (Result, bool) {
		if s.MaxInListLen <= maxLen {
			return Result{}, false
		}
		return Result{
			RuleName:   "in-list-too-large",
			Query:      s.Raw,
			Message:    fmt.Sprintf("IN list has %d elements (threshold %d). Large IN lists hurt query planning.", s.MaxInListLen, maxLen),
			Suggestion: "Use a JOIN against a temp table / VALUES list, or a parameterized array such as = ANY($1).",
		}, true
	}
}

// CheckSelectDistinct detects a select-level DISTINCT, which is often added to
// hide duplicate rows produced by an unintended join fan-out rather than to
// express a genuine need for distinct results. INFO by default.
func CheckSelectDistinct(s *Statement) (Result, bool) {
	if s.SelectDistinct {
		return Result{
			RuleName:   "select-distinct",
			Query:      s.Raw,
			Message:    "SELECT DISTINCT detected. It often masks duplicate rows from an unintended join.",
			Suggestion: "Confirm the duplicates aren't a join fan-out; prefer fixing the join or using EXISTS/GROUP BY.",
		}, true
	}
	return Result{}, false
}

// CheckLargeOffset detects a literal OFFSET larger than the default threshold
// (1000). Use the registry / config to tune threshold.
func CheckLargeOffset(s *Statement) (Result, bool) {
	return largeOffsetRule(1000)(s)
}

// largeOffsetRule builds the large-offset rule. It flags a statement whose
// literal OFFSET exceeds threshold — deep pagination, where the database scans
// and discards every skipped row. Parameterized offsets (OffsetValue == 0) are
// never flagged.
func largeOffsetRule(threshold int) Rule {
	return func(s *Statement) (Result, bool) {
		if s.OffsetValue <= threshold {
			return Result{}, false
		}
		return Result{
			RuleName:   "large-offset",
			Query:      s.Raw,
			Message:    fmt.Sprintf("OFFSET %d exceeds %d. Deep pagination scans and discards all skipped rows.", s.OffsetValue, threshold),
			Suggestion: "Use keyset (cursor) pagination: WHERE id > $last ORDER BY id LIMIT n.",
		}, true
	}
}

// CheckCartesianJoin detects a multi-table FROM with no join condition and no
// WHERE filter — an unconditioned cartesian product (incl. CROSS JOIN).
func CheckCartesianJoin(s *Statement) (Result, bool) {
	if s.CartesianJoin {
		return Result{
			RuleName:   "cartesian-join",
			Query:      s.Raw,
			Message:    "Cartesian product: multiple tables joined with no join condition or WHERE filter.",
			Suggestion: "Add a JOIN ... ON condition (or a WHERE clause relating the tables).",
		}, true
	}
	return Result{}, false
}

// CheckImplicitJoin detects a FROM clause that joins tables with commas
// (FROM a, b) instead of explicit JOIN syntax — error-prone because a
// forgotten join condition silently yields a cartesian product.
func CheckImplicitJoin(s *Statement) (Result, bool) {
	if s.ImplicitCommaJoin {
		return Result{
			RuleName:   "implicit-join",
			Query:      s.Raw,
			Message:    "Implicit comma join in FROM. A missing join condition silently becomes a cartesian product.",
			Suggestion: "Use explicit JOIN ... ON syntax.",
		}, true
	}
	return Result{}, false
}

// CheckOrderByWithoutLimit detects ORDER BY without LIMIT, which sorts the
// entire result set.
func CheckOrderByWithoutLimit(s *Statement) (Result, bool) {
	if s.HasOrderBy && !s.HasLimit {
		return Result{
			RuleName:   "orderby-without-limit",
			Query:      s.Raw,
			Message:    "ORDER BY without LIMIT sorts the entire result set.",
			Suggestion: "Add a LIMIT clause if you only need a subset of rows.",
		}, true
	}
	return Result{}, false
}
