package pgparser

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
			name: "cte-wrapped delete with where",
			sql:  "WITH r AS (SELECT id FROM o WHERE ts > now()) DELETE FROM o WHERE id IN (SELECT id FROM r)",
			want: analyzer.Statement{Kind: analyzer.StmtDelete, HasWhere: true, Exact: true},
		},
		{
			name: "delete without where",
			sql:  "DELETE FROM users",
			want: analyzer.Statement{Kind: analyzer.StmtDelete, HasWhere: false, Exact: true},
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
			sql:  "SELECT count(*) FROM users",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, SelectStar: false, HasFrom: true, Exact: true},
		},
		{
			name: "select no from",
			sql:  "SELECT 1",
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
			want: analyzer.Statement{Kind: analyzer.StmtInsert, InsertColumnsListed: false, Exact: true},
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
			name: "distinct on",
			sql:  "SELECT DISTINCT ON (dept) dept, name FROM emp",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, SelectDistinct: true, Exact: true},
		},
		{
			name: "count distinct is not select distinct",
			sql:  "SELECT count(DISTINCT id) FROM users",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, Exact: true},
		},
		{
			name: "literal offset",
			sql:  "SELECT id FROM users WHERE x = 1 ORDER BY id LIMIT 10 OFFSET 5000",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasWhere: true, HasOrderBy: true, HasLimit: true, OffsetValue: 5000, Exact: true},
		},
		{
			name: "parameterized offset is zero",
			sql:  "SELECT id FROM users WHERE x = 1 LIMIT 10 OFFSET $1",
			want: analyzer.Statement{Kind: analyzer.StmtSelect, HasFrom: true, HasWhere: true, HasLimit: true, Exact: true},
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
	// Driver placeholders the PG grammar won't accept as-is still must not
	// error, and must come back as a best-effort (non-exact) Statement.
	st, err := p.Parse("SELECT * FROM t WHERE id = ?")
	if err != nil {
		t.Fatalf("fallback path must not error: %v", err)
	}
	if st == nil {
		t.Fatal("nil statement")
	}
	if st.Exact {
		t.Error("expected Exact=false when grammar rejected the SQL")
	}
}

func TestParser_IntegratesWithAnalyzer(t *testing.T) {
	a := analyzer.Default().WithParser(New())

	got := a.Analyze("DELETE FROM users -- WHERE id = 1")
	if len(got) != 1 || got[0].RuleName != "delete-without-where" {
		t.Errorf("expected delete-without-where (WHERE only in comment), got %+v", got)
	}

	if r := a.Analyze("SELECT id FROM users WHERE id = 1 LIMIT 1"); len(r) != 0 {
		t.Errorf("expected no findings for safe query, got %+v", r)
	}
}
