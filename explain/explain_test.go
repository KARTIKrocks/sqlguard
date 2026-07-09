package explain

import (
	"strings"
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestValidate(t *testing.T) {
	// wantKind is asserted on every case, including the rejected ones: the kind
	// drives isDML, which picks the transaction mode in analyzeMySQL. A
	// classification regression there would silently reintroduce MySQL error
	// 1792 (a DML EXPLAIN inside a READ ONLY transaction), which no unit test
	// can observe.
	cases := []struct {
		name     string
		query    string
		allowDML bool
		wantErr  string // substring; "" means no error
		wantSafe string // expected returned statement when no error
		wantKind analyzer.StmtKind
	}{
		{"select ok", `SELECT * FROM t WHERE id = 1`, false, "", `SELECT * FROM t WHERE id = 1`, analyzer.StmtSelect},
		{"with ok", `WITH c AS (SELECT 1) SELECT * FROM c`, false, "", `WITH c AS (SELECT 1) SELECT * FROM c`, analyzer.StmtSelect},
		{"trailing semicolon trimmed", `SELECT 1;`, false, "", `SELECT 1`, analyzer.StmtSelect},
		{"empty", `   `, false, "empty", "", analyzer.StmtUnknown},
		{"stacked statements", `SELECT 1; DROP TABLE users`, false, "multi-statement", "", analyzer.StmtUnknown},
		{"semicolon in comment is fine", "SELECT 1 -- ; DROP\n", false, "", "SELECT 1 -- ; DROP", analyzer.StmtSelect},
		{"semicolon in string is fine", `SELECT * FROM t WHERE s = 'a;b'`, false, "", `SELECT * FROM t WHERE s = 'a;b'`, analyzer.StmtSelect},
		{"stack hidden after string", `SELECT 'a;b'; DELETE FROM t`, false, "multi-statement", "", analyzer.StmtUnknown},
		{"dml refused by default", `DELETE FROM t WHERE id = 1`, false, "data-modifying", "", analyzer.StmtDelete},
		{"update refused by default", `UPDATE t SET a = 1`, false, "data-modifying", "", analyzer.StmtUpdate},
		{"insert refused by default", `INSERT INTO t (a) VALUES (1)`, false, "data-modifying", "", analyzer.StmtInsert},
		{"dml allowed with opt-in", `DELETE FROM t WHERE id = 1`, true, "", `DELETE FROM t WHERE id = 1`, analyzer.StmtDelete},
		{"update allowed with opt-in", `UPDATE t SET a = 1`, true, "", `UPDATE t SET a = 1`, analyzer.StmtUpdate},
		// A CTE-wrapped DML statement starts with WITH but must not classify as
		// SELECT: on MySQL that would leave it in a READ ONLY transaction.
		{"cte-wrapped delete is dml", `WITH c AS (SELECT 1) DELETE FROM t WHERE id = 1`, true, "", `WITH c AS (SELECT 1) DELETE FROM t WHERE id = 1`, analyzer.StmtDelete},
		{"cte-wrapped update is dml", `WITH c AS (SELECT 1) UPDATE t SET a = 1`, true, "", `WITH c AS (SELECT 1) UPDATE t SET a = 1`, analyzer.StmtUpdate},
		{"cte-wrapped insert is dml", `WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c`, true, "", `WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c`, analyzer.StmtInsert},
		{"cte-wrapped delete refused by default", `WITH c AS (SELECT 1) DELETE FROM t WHERE id = 1`, false, "data-modifying", "", analyzer.StmtDelete},
		{"ddl always refused", `DROP TABLE users`, true, "non-SELECT", "", analyzer.StmtOther},
		{"set always refused", `SET search_path = x`, true, "non-SELECT", "", analyzer.StmtOther},
		{"truncate refused", `TRUNCATE t`, true, "non-SELECT", "", analyzer.StmtOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &PlanAnalyzer{dialect: "postgres", allowDML: c.allowDML}
			safe, kind, err := p.validate(c.query)

			// Asserted on both paths: a rejected DML statement must still be
			// classified as DML, not folded into StmtUnknown.
			if kind != c.wantKind {
				t.Errorf("kind = %v, want %v", kind, c.wantKind)
			}
			if isDML(kind) != isDML(c.wantKind) {
				t.Errorf("isDML(%v) = %v, want %v", kind, isDML(kind), isDML(c.wantKind))
			}

			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if safe != c.wantSafe {
					t.Errorf("safe = %q, want %q", safe, c.wantSafe)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not contain %q", err, c.wantErr)
			}
		})
	}
}
