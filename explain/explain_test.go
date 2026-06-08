package explain

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		allowDML bool
		wantErr  string // substring; "" means no error
		wantSafe string // expected returned statement when no error
	}{
		{"select ok", `SELECT * FROM t WHERE id = 1`, false, "", `SELECT * FROM t WHERE id = 1`},
		{"with ok", `WITH c AS (SELECT 1) SELECT * FROM c`, false, "", `WITH c AS (SELECT 1) SELECT * FROM c`},
		{"trailing semicolon trimmed", `SELECT 1;`, false, "", `SELECT 1`},
		{"empty", `   `, false, "empty", ""},
		{"stacked statements", `SELECT 1; DROP TABLE users`, false, "multi-statement", ""},
		{"semicolon in comment is fine", "SELECT 1 -- ; DROP\n", false, "", "SELECT 1 -- ; DROP"},
		{"semicolon in string is fine", `SELECT * FROM t WHERE s = 'a;b'`, false, "", `SELECT * FROM t WHERE s = 'a;b'`},
		{"stack hidden after string", `SELECT 'a;b'; DELETE FROM t`, false, "multi-statement", ""},
		{"dml refused by default", `DELETE FROM t WHERE id = 1`, false, "data-modifying", ""},
		{"update refused by default", `UPDATE t SET a = 1`, false, "data-modifying", ""},
		{"dml allowed with opt-in", `DELETE FROM t WHERE id = 1`, true, "", `DELETE FROM t WHERE id = 1`},
		{"ddl always refused", `DROP TABLE users`, true, "non-SELECT", ""},
		{"set always refused", `SET search_path = x`, true, "non-SELECT", ""},
		{"truncate refused", `TRUNCATE t`, true, "non-SELECT", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &PlanAnalyzer{dialect: "postgres", allowDML: c.allowDML}
			safe, err := p.validate(c.query)
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
