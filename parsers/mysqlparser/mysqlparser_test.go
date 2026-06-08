package mysqlparser

import (
	"testing"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

func TestParser_ExactStructuralFacts(t *testing.T) {
	p := New()
	tests := []struct {
		name string
		sql  string
		want analyzer.Statement
	}{
		{
			name: "delete without where",
			sql:  "DELETE FROM users",
			want: analyzer.Statement{Kind: analyzer.StmtDelete, Exact: true},
		},
		{
			name: "delete with where",
			sql:  "DELETE FROM users WHERE id = 1",
			want: analyzer.Statement{Kind: analyzer.StmtDelete, HasWhere: true, Exact: true},
		},
		{
			name: "update without where",
			sql:  "UPDATE users SET name = 'x'",
			want: analyzer.Statement{Kind: analyzer.StmtUpdate, Exact: true},
		},
		{
			name: "select star with from",
			sql:  "SELECT * FROM users",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, SelectStar: true, HasFrom: true, Exact: true},
		},
		{
			name: "qualified star",
			sql:  "SELECT u.* FROM users u",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, SelectStar: true, HasFrom: true, Exact: true},
		},
		{
			name: "count star is not select star",
			sql:  "SELECT COUNT(*) FROM users WHERE id = 1",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasWhere: true, Exact: true},
		},
		{
			name: "select 1 has no real from",
			sql:  "SELECT 1",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: false, Exact: true},
		},
		{
			name: "explicit dual is not a real from",
			sql:  "SELECT 1 FROM dual",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: false, Exact: true},
		},
		{
			name: "uppercase DUAL is not a real from",
			sql:  "SELECT 1 FROM DUAL",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: false, Exact: true},
		},
		{
			name: "backticked DUAL is not a real from",
			sql:  "SELECT 1 FROM `DUAL`",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: false, Exact: true},
		},
		{
			name: "insert with columns",
			sql:  "INSERT INTO users (name) VALUES ('a')",
			want: analyzer.Statement{Kind: analyzer.StmtInsert, InsertColumnsListed: true, Exact: true},
		},
		{
			name: "insert without columns",
			sql:  "INSERT INTO users VALUES ('a')",
			want: analyzer.Statement{Kind: analyzer.StmtInsert, Exact: true},
		},
		{
			name: "order by without limit",
			sql:  "SELECT id FROM users ORDER BY name",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasOrderBy: true, Exact: true},
		},
		{
			name: "select distinct",
			sql:  "SELECT DISTINCT name FROM users",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, SelectDistinct: true, Exact: true},
		},
		{
			name: "count distinct is not select distinct",
			sql:  "SELECT COUNT(DISTINCT id) FROM users WHERE id = 1",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasWhere: true, Exact: true},
		},
		{
			name: "literal offset (OFFSET form)",
			sql:  "SELECT id FROM users WHERE x = 1 ORDER BY id LIMIT 10 OFFSET 5000",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasWhere: true, HasOrderBy: true, HasLimit: true, OffsetValue: 5000, Exact: true},
		},
		{
			name: "literal offset (LIMIT n, count form)",
			sql:  "SELECT id FROM users WHERE x = 1 LIMIT 5000, 10",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasWhere: true, HasLimit: true, OffsetValue: 5000, Exact: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := p.Parse(tt.sql)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if st.Kind != tt.want.Kind ||
				st.HasWhere != tt.want.HasWhere ||
				st.HasLimit != tt.want.HasLimit ||
				st.HasOrderBy != tt.want.HasOrderBy ||
				st.HasFrom != tt.want.HasFrom ||
				st.SelectStar != tt.want.SelectStar ||
				st.SelectDistinct != tt.want.SelectDistinct ||
				st.OffsetValue != tt.want.OffsetValue ||
				st.InsertColumnsListed != tt.want.InsertColumnsListed ||
				st.Exact != tt.want.Exact {
				t.Errorf("Parse(%q)\n got: %+v\nwant: %+v", tt.sql, *st, tt.want)
			}
		})
	}
}

func TestParser_FallsBackOnUnparseable(t *testing.T) {
	p := New()
	// Postgres-style placeholders the MySQL grammar rejects must not error
	// and must come back as a best-effort (non-exact) Statement.
	st, err := p.Parse("SELECT * FROM t WHERE id = $1")
	if err != nil {
		t.Fatalf("fallback path must not error: %v", err)
	}
	if st == nil || st.Exact {
		t.Errorf("expected non-nil, non-exact fallback statement, got %+v", st)
	}
}

func TestParser_IntegratesWithAnalyzer(t *testing.T) {
	a := analyzer.Default().WithParser(New())

	got := a.Analyze("UPDATE users SET active = 0 /* WHERE id = 1 */")
	found := false
	for _, r := range got {
		if r.RuleName == "update-without-where" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected update-without-where (WHERE only in comment), got %+v", got)
	}
}
