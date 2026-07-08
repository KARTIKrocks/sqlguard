// Package explain provides SQL EXPLAIN plan analysis.
// It connects to a live database to run EXPLAIN on queries and detect
// performance issues like sequential scans and high-cost operations.
package explain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	safe, err := p.validate(query)
	if err != nil {
		return nil, err
	}

	var res *Result
	switch p.dialect {
	case "postgres":
		res, err = p.analyzePostgres(ctx, safe)
	case "mysql":
		res, err = p.analyzeMySQL(ctx, safe)
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
func (p *PlanAnalyzer) validate(query string) (string, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", fmt.Errorf("explain: refusing to explain an empty query")
	}
	if analyzer.IsMultiStatement(q) {
		return "", fmt.Errorf("explain: refusing to explain multi-statement input")
	}
	q = strings.TrimRight(q, "; \t\r\n")

	st, _ := analyzer.NewFallbackParser().Parse(q)
	switch st.Kind {
	case analyzer.StmtSelect:
		return q, nil
	case analyzer.StmtInsert, analyzer.StmtUpdate, analyzer.StmtDelete:
		if !p.allowDML {
			return "", fmt.Errorf("explain: refusing to EXPLAIN a data-modifying statement by default; construct the analyzer with explain.WithAllowDML to opt in")
		}
		return q, nil
	default:
		return "", fmt.Errorf("explain: refusing to explain a non-SELECT/WITH/DML statement (DDL, SET, transaction control, or unrecognized)")
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

func (p *PlanAnalyzer) analyzeMySQL(ctx context.Context, query string) (*Result, error) {
	// See analyzePostgres: validated single statement, no ANALYZE, run in an
	// always-rolled-back read-only transaction so EXPLAIN cannot mutate data.
	//nolint:gosec // G202: EXPLAIN takes no bind params, so concatenation is by
	// design; the defense is validate() + the rolled-back read-only tx, not
	// parameterization.
	explainQuery := "EXPLAIN " + query

	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("explain: failed to begin read-only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, explainQuery)
	if err != nil {
		return nil, fmt.Errorf("explain: failed to run EXPLAIN: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := &Result{
		Query: query,
	}

	for rows.Next() {
		var (
			id           int
			selectType   string
			table        sql.NullString
			partitions   sql.NullString
			accessType   sql.NullString
			possibleKeys sql.NullString
			key          sql.NullString
			keyLen       sql.NullString
			ref          sql.NullString
			rowCount     sql.NullInt64
			filtered     sql.NullFloat64
			extra        sql.NullString
		)

		if err := rows.Scan(&id, &selectType, &table, &partitions, &accessType, &possibleKeys, &key, &keyLen, &ref, &rowCount, &filtered, &extra); err != nil {
			return result, fmt.Errorf("explain: failed to scan row: %w", err)
		}

		// Detect full table scans (type = ALL)
		if accessType.Valid && accessType.String == "ALL" {
			result.Issues = append(result.Issues, analyzer.Result{
				RuleName:   "full-table-scan",
				Severity:   analyzer.SeverityWarning,
				Query:      query,
				Message:    fmt.Sprintf("Full table scan on %s (estimated %d rows)", table.String, rowCount.Int64),
				Suggestion: "Consider adding an index to avoid full table scan.",
			})
		}

		// Detect missing indexes
		if (!key.Valid || key.String == "") && (!possibleKeys.Valid || possibleKeys.String == "") && table.Valid && table.String != "" {
			result.Issues = append(result.Issues, analyzer.Result{
				RuleName:   "no-index-used",
				Severity:   analyzer.SeverityWarning,
				Query:      query,
				Message:    fmt.Sprintf("No index used on table %s", table.String),
				Suggestion: "Consider adding an index on the filtered/joined columns.",
			})
		}

		// Detect filesort
		if strings.Contains(extra.String, "Using filesort") {
			result.Issues = append(result.Issues, analyzer.Result{
				RuleName:   "filesort",
				Severity:   analyzer.SeverityInfo,
				Query:      query,
				Message:    fmt.Sprintf("Filesort detected on table %s", table.String),
				Suggestion: "Consider adding an index that covers the ORDER BY columns.",
			})
		}
	}

	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("explain: error reading rows: %w", err)
	}

	return result, nil
}
