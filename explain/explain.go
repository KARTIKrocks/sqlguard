// Package explain provides SQL EXPLAIN plan analysis.
// It connects to a live database to run EXPLAIN on queries and detect
// performance issues like sequential scans and high-cost operations.
package explain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// PlanAnalyzer runs EXPLAIN on queries against a live database.
type PlanAnalyzer struct {
	db       *sql.DB
	dialect  string // "postgres" or "mysql"
	allowDML bool
}

// Option configures a PlanAnalyzer.
type Option func(*PlanAnalyzer)

// WithAllowDML permits EXPLAIN on INSERT/UPDATE/DELETE statements. It is OFF
// by default: only SELECT/WITH are explained, because feeding DML to a prod
// database — even under plain EXPLAIN — is a footgun (and EXPLAIN ANALYZE
// would execute it). When enabled, DML EXPLAINs still run inside a
// transaction that is always rolled back (see analyzePostgres/analyzeMySQL),
// so nothing is committed regardless.
func WithAllowDML() Option {
	return func(p *PlanAnalyzer) { p.allowDML = true }
}

// New creates a PlanAnalyzer for the given database connection.
// dialect must be "postgres" or "mysql".
func New(db *sql.DB, dialect string, opts ...Option) (*PlanAnalyzer, error) {
	if db == nil {
		return nil, fmt.Errorf("explain: db is nil")
	}
	dialect = strings.ToLower(dialect)
	if dialect != "postgres" && dialect != "mysql" {
		return nil, fmt.Errorf("explain: unsupported dialect %q (use 'postgres' or 'mysql')", dialect)
	}
	p := &PlanAnalyzer{db: db, dialect: dialect}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// Result holds the parsed EXPLAIN output and any detected issues.
type Result struct {
	Query   string
	RawPlan string
	Issues  []analyzer.Result
}

// Analyze runs EXPLAIN on the given query and returns detected issues. The
// query is validated (see validate) and the EXPLAIN is run inside an
// always-rolled-back transaction, so a query passed here cannot mutate the
// target database.
func (p *PlanAnalyzer) Analyze(ctx context.Context, query string) (*Result, error) {
	safe, kind, err := p.validate(query)
	if err != nil {
		return nil, err
	}

	var res *Result
	switch p.dialect {
	case "postgres":
		res, err = p.analyzePostgres(ctx, safe)
	case "mysql":
		res, err = p.analyzeMySQL(ctx, safe, isDML(kind))
	default:
		return nil, fmt.Errorf("explain: unsupported dialect %q", p.dialect)
	}
	if res != nil {
		fp := analyzer.Fingerprint(query)
		for i := range res.Issues {
			res.Issues[i].Fingerprint = fp
		}
	}
	return res, err
}

// validate enforces the EXPLAIN safety policy and returns the single,
// terminator-stripped statement that is safe to concatenate into an EXPLAIN
// prefix.
//
// EXPLAIN cannot take bind parameters, so the query is necessarily
// string-concatenated; the defense is therefore strict input validation, not
// parameterization:
//
//   - Reject empty input.
//   - Reject multi-statement input using a comment- and string-literal-aware
//     check (analyzer.IsMultiStatement). The previous
//     strings.Contains(query, ";") check was defeated by a ";" inside a
//     -- / /* */ comment or a string literal, and over-rejected a harmless
//     trailing ";".
//   - Classify the statement via the same parser the analyzer uses. Only
//     SELECT/WITH are allowed by default; INSERT/UPDATE/DELETE require
//     WithAllowDML; DDL/SET/other is always refused.
func (p *PlanAnalyzer) validate(query string) (string, analyzer.StmtKind, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", analyzer.StmtUnknown, fmt.Errorf("explain: refusing to explain an empty query")
	}
	if analyzer.IsMultiStatement(q) {
		return "", analyzer.StmtUnknown, fmt.Errorf("explain: refusing to explain multi-statement input")
	}
	q = strings.TrimRight(q, "; \t\r\n")

	st, _ := analyzer.NewFallbackParser().Parse(q)
	switch st.Kind {
	case analyzer.StmtSelect:
		return q, st.Kind, nil
	case analyzer.StmtInsert, analyzer.StmtUpdate, analyzer.StmtDelete:
		if !p.allowDML {
			return "", st.Kind, fmt.Errorf("explain: refusing to EXPLAIN a data-modifying statement by default; construct the analyzer with explain.WithAllowDML to opt in")
		}
		return q, st.Kind, nil
	default:
		return "", st.Kind, fmt.Errorf("explain: refusing to explain a non-SELECT/WITH/DML statement (DDL, SET, transaction control, or unrecognized)")
	}
}

// isDML reports whether kind is a data-modifying statement.
func isDML(kind analyzer.StmtKind) bool {
	switch kind {
	case analyzer.StmtInsert, analyzer.StmtUpdate, analyzer.StmtDelete:
		return true
	default:
		return false
	}
}

// PostgreSQL EXPLAIN JSON structures
type pgPlan struct {
	Plan pgPlanNode `json:"Plan"`
}

type pgPlanNode struct {
	NodeType  string       `json:"Node Type"`
	TotalCost float64      `json:"Total Cost"`
	PlanRows  int64        `json:"Plan Rows"`
	Plans     []pgPlanNode `json:"Plans"`
}

func (p *PlanAnalyzer) analyzePostgres(ctx context.Context, query string) (*Result, error) {
	// query is the validated, single, terminator-free statement from
	// validate(). EXPLAIN takes no bind parameters, so concatenation is
	// unavoidable; safety comes from validate() plus the rolled-back,
	// read-only transaction below. We never use EXPLAIN ANALYZE, so the
	// statement is planned, not executed.
	//nolint:gosec // G202: EXPLAIN takes no bind params, so concatenation is by
	// design; the defense is validate() + the rolled-back read-only tx, not
	// parameterization.
	explainQuery := "EXPLAIN (FORMAT JSON) " + query

	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("explain: failed to begin read-only transaction: %w", err)
	}
	// Always roll back: an EXPLAIN must never commit anything.
	defer func() { _ = tx.Rollback() }()

	var rawJSON string
	if err := tx.QueryRowContext(ctx, explainQuery).Scan(&rawJSON); err != nil {
		return nil, fmt.Errorf("explain: failed to run EXPLAIN: %w", err)
	}

	result := &Result{
		Query:   query,
		RawPlan: rawJSON,
	}

	var plans []pgPlan
	if err := json.Unmarshal([]byte(rawJSON), &plans); err != nil {
		return result, fmt.Errorf("explain: failed to parse EXPLAIN JSON: %w", err)
	}

	if len(plans) > 0 {
		p.walkPgPlan(&plans[0].Plan, query, &result.Issues)
	}

	return result, nil
}

func (p *PlanAnalyzer) walkPgPlan(node *pgPlanNode, query string, issues *[]analyzer.Result) {
	if node == nil {
		return
	}

	// Detect sequential scans
	if node.NodeType == "Seq Scan" {
		severity := analyzer.SeverityInfo
		if node.PlanRows > 1000 {
			severity = analyzer.SeverityWarning
		}
		*issues = append(*issues, analyzer.Result{
			RuleName:   "seq-scan",
			Severity:   severity,
			Query:      query,
			Message:    fmt.Sprintf("Sequential scan detected (estimated %d rows, cost %.1f)", node.PlanRows, node.TotalCost),
			Suggestion: "Consider adding an index to avoid full table scan.",
		})
	}

	// Detect high cost operations
	if node.TotalCost > 10000 {
		*issues = append(*issues, analyzer.Result{
			RuleName:   "high-cost",
			Severity:   analyzer.SeverityWarning,
			Query:      query,
			Message:    fmt.Sprintf("High cost operation: %s (cost %.1f)", node.NodeType, node.TotalCost),
			Suggestion: "Review query plan and consider optimization.",
		})
	}

	// Recurse into child plans
	for i := range node.Plans {
		p.walkPgPlan(&node.Plans[i], query, issues)
	}
}

func (p *PlanAnalyzer) analyzeMySQL(ctx context.Context, query string, dml bool) (*Result, error) {
	// See analyzePostgres: validated single statement, no ANALYZE, run in an
	// always-rolled-back transaction so EXPLAIN cannot mutate data.
	//
	// FORMAT=TRADITIONAL pins the tabular plan. MySQL 9 defaults
	// @@explain_format to TREE, which yields one free-text column and no
	// per-table rows to inspect; the clause restores the classic output and is
	// also accepted by MySQL 5.7/8 and MariaDB.
	//
	//nolint:gosec // G202: EXPLAIN takes no bind params, so concatenation is by
	// design; the defense is validate() + the rolled-back tx, not
	// parameterization.
	explainQuery := "EXPLAIN FORMAT=TRADITIONAL " + query

	// MySQL and MariaDB reject *any* statement inside a READ ONLY transaction
	// (error 1792), including an EXPLAIN that only plans it. Fall back to a
	// regular transaction for DML: plain EXPLAIN never executes the statement,
	// and the rollback below is what guarantees nothing is committed.
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !dml})
	if err != nil {
		return nil, fmt.Errorf("explain: failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, explainQuery)
	if err != nil {
		return nil, fmt.Errorf("explain: failed to run EXPLAIN: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// MySQL emits 12 columns, MariaDB 10 (no `partitions`, no `filtered`).
	// Address them by name so a differing column set is not a scan error.
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("explain: failed to read EXPLAIN columns: %w", err)
	}
	index := make(map[string]int, len(cols))
	for i, c := range cols {
		index[strings.ToLower(c)] = i
	}
	cells := make([]sql.NullString, len(cols))
	scanArgs := make([]any, len(cols))
	for i := range cells {
		scanArgs[i] = &cells[i]
	}
	col := func(name string) string {
		if i, ok := index[name]; ok {
			return cells[i].String
		}
		return ""
	}

	result := &Result{
		Query: query,
	}

	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			return result, fmt.Errorf("explain: failed to scan row: %w", err)
		}
		result.Issues = append(result.Issues, mysqlRowIssues(query, col)...)
	}

	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("explain: error reading rows: %w", err)
	}

	return result, nil
}

// mysqlRowIssues applies the plan rules to a single EXPLAIN row. col resolves a
// lower-cased column name to its value, or "" when the server does not emit it.
func mysqlRowIssues(query string, col func(string) string) []analyzer.Result {
	// A UNION RESULT or derived-table row names a temporary table such as
	// `<union1,2>`, never a real one. It has no index by construction, so the
	// rules below would only produce noise.
	table := col("table")
	if table == "" || strings.HasPrefix(table, "<") {
		return nil
	}

	var issues []analyzer.Result

	// Detect full table scans (type = ALL)
	if col("type") == "ALL" {
		planRows, _ := strconv.ParseInt(col("rows"), 10, 64)
		issues = append(issues, analyzer.Result{
			RuleName:   "full-table-scan",
			Severity:   analyzer.SeverityWarning,
			Query:      query,
			Message:    fmt.Sprintf("Full table scan on %s (estimated %d rows)", table, planRows),
			Suggestion: "Consider adding an index to avoid full table scan.",
		})
	}

	// Detect missing indexes
	if col("key") == "" && col("possible_keys") == "" {
		issues = append(issues, analyzer.Result{
			RuleName:   "no-index-used",
			Severity:   analyzer.SeverityWarning,
			Query:      query,
			Message:    fmt.Sprintf("No index used on table %s", table),
			Suggestion: "Consider adding an index on the filtered/joined columns.",
		})
	}

	// Detect filesort
	if strings.Contains(col("extra"), "Using filesort") {
		issues = append(issues, analyzer.Result{
			RuleName:   "filesort",
			Severity:   analyzer.SeverityInfo,
			Query:      query,
			Message:    fmt.Sprintf("Filesort detected on table %s", table),
			Suggestion: "Consider adding an index that covers the ORDER BY columns.",
		})
	}

	return issues
}
