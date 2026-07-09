//go:build integration

package integration

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/explain"
)

// setupPostgres creates two unindexed-by-name tables with enough rows that the
// planner's estimates cross the seq-scan and high-cost thresholds.
func setupPostgres(t *testing.T) *sql.DB {
	t.Helper()
	db := connect(t, envPostgres, "pgx")
	exec(t, db,
		`DROP TABLE IF EXISTS orders, users`,
		`CREATE TABLE users (id serial PRIMARY KEY, email text, name text)`,
		`CREATE TABLE orders (id serial PRIMARY KEY, user_id int, total numeric)`,
		`INSERT INTO users (email, name) SELECT 'e' || g, 'n' || g FROM generate_series(1, 5000) g`,
		`INSERT INTO orders (user_id, total) SELECT g % 5000 + 1, g FROM generate_series(1, 5000) g`,
		`ANALYZE users`,
		`ANALYZE orders`,
	)
	return db
}

// A predicate on an unindexed column must plan as a Seq Scan, and an estimate
// above 1000 rows must escalate the severity from INFO to WARNING.
func TestPostgres_SeqScanOnUnindexedColumn(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	res, err := p.Analyze(testCtx(t), `SELECT * FROM users WHERE name LIKE 'n1%'`)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !hasRule(res.Issues, "seq-scan") {
		t.Fatalf("expected seq-scan, got %v", ruleNames(res.Issues))
	}
	for _, r := range res.Issues {
		if r.RuleName != "seq-scan" {
			continue
		}
		if r.Severity != analyzer.SeverityWarning {
			t.Errorf("severity = %v, want WARNING (estimate exceeds 1000 rows)", r.Severity)
		}
	}
}

// The primary-key lookup plans as an Index Scan, so no seq-scan is reported.
// This is the negative control for the test above.
func TestPostgres_IndexedLookupHasNoSeqScan(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	res, err := p.Analyze(testCtx(t), `SELECT * FROM users WHERE id = 1`)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if hasRule(res.Issues, "seq-scan") {
		t.Errorf("unexpected seq-scan on a primary-key lookup: %v", ruleNames(res.Issues))
	}
}

// A join over two unindexed columns nests one Seq Scan under another inside
// "Plans", so more than one issue proves walkPgPlan actually recurses.
func TestPostgres_WalkRecursesIntoChildPlans(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	res, err := p.Analyze(testCtx(t), `SELECT * FROM users u JOIN orders o ON o.user_id = u.id`)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var seqScans int
	for _, r := range res.Issues {
		if r.RuleName == "seq-scan" {
			seqScans++
		}
	}
	if seqScans < 2 {
		t.Errorf("seq-scan count = %d, want >= 2 (one per joined table); issues %v", seqScans, ruleNames(res.Issues))
	}
}

// A cartesian product pushes Total Cost past the 10000 threshold.
func TestPostgres_HighCost(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	res, err := p.Analyze(testCtx(t), `SELECT * FROM users u, orders o`)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !hasRule(res.Issues, "high-cost") {
		t.Errorf("expected high-cost on a 5000x5000 cartesian product, got %v", ruleNames(res.Issues))
	}
}

// RawPlan must be the verbatim server response: a JSON array whose first
// element carries a "Plan" object with the keys pgPlanNode unmarshals. This is
// the contract that would silently break on a Postgres major upgrade.
func TestPostgres_RawPlanJSONContract(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	res, err := p.Analyze(testCtx(t), `SELECT * FROM users WHERE name = 'n1'`)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	var plans []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(res.RawPlan), &plans); err != nil {
		t.Fatalf("RawPlan is not a JSON array: %v\n%s", err, res.RawPlan)
	}
	if len(plans) != 1 {
		t.Fatalf("len(plans) = %d, want 1", len(plans))
	}
	for _, key := range []string{"Node Type", "Total Cost", "Plan Rows"} {
		if _, ok := plans[0].Plan[key]; !ok {
			t.Errorf("plan is missing key %q; pgPlanNode would decode a zero value", key)
		}
	}
}

// Every issue carries the fingerprint of the original query text.
func TestPostgres_IssuesCarryFingerprint(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	const q = `SELECT * FROM users WHERE name LIKE 'n1%'`
	res, err := p.Analyze(testCtx(t), q)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(res.Issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	want := analyzer.Fingerprint(q)
	for _, r := range res.Issues {
		if r.Fingerprint != want {
			t.Errorf("Fingerprint = %q, want %q", r.Fingerprint, want)
		}
	}
}

// Without WithAllowDML, a DELETE never reaches the server.
func TestPostgres_DMLRefusedByDefault(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres")

	_, err := p.Analyze(testCtx(t), `DELETE FROM users WHERE id = 1`)
	if err == nil {
		t.Fatal("expected an error without WithAllowDML")
	}
	if !strings.Contains(err.Error(), "data-modifying") {
		t.Errorf("error = %v, want it to mention a data-modifying statement", err)
	}
}

// Postgres permits EXPLAIN of a DELETE inside a READ ONLY transaction (it
// plans without executing), and the rollback leaves every row in place.
func TestPostgres_ExplainDMLDoesNotMutate(t *testing.T) {
	db := setupPostgres(t)
	p := analyzerFor(t, db, "postgres", explain.WithAllowDML())

	var before int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	if _, err := p.Analyze(testCtx(t), `DELETE FROM users WHERE id < 100`); err != nil {
		t.Fatalf("Analyze(DELETE) with WithAllowDML: %v", err)
	}

	var after int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if before != after {
		t.Errorf("row count changed from %d to %d; EXPLAIN must never mutate", before, after)
	}
}
