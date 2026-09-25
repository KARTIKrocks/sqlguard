package mysqlparser

import (
	"strings"
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

// TestParser_NeverAddsFindingTheFallbackDoesNot pins the direction of the
// trade-off website/docs/parsers.md promises: opting into the real grammar
// removes findings the heuristics guessed wrong, and never adds one the
// zero-dependency default would not have produced. A statement kind the
// grammar understands but the heuristics did not is the shape that breaks it —
// vitess parses REPLACE into the same node as INSERT, so it was reported
// against a statement the fallback read as StmtOther (#68).
//
// Some rows below need matching support in the core's fallback parser; see
// fallbackKnowsInsertLikeKeywords for why they are skipped against an older
// core rather than failing.
func TestParser_NeverAddsFindingTheFallbackDoesNot(t *testing.T) {
	// Fallback-only findings are expected and allowed: they are the false
	// positives the grammar exists to drop.
	corpus := []string{
		"INSERT INTO t SET a = 1",
		"INSERT INTO t (a, b) VALUES (1, 2)",
		"INSERT INTO t VALUES (1, 2)",
		"INSERT INTO t (a) SELECT x FROM u",
		"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = 2",
		"REPLACE INTO t (a) VALUES (1)",
		"REPLACE INTO t VALUES (1)",
		// INTO is optional in MySQL for both keywords, and the modifier run may
		// sit between the keyword and the table. These are the shapes an
		// INTO-anchored heuristic misses while the grammar still reports them.
		"REPLACE t VALUES (1)",
		"REPLACE t (a) VALUES (1)",
		"INSERT t VALUES (1)",
		"INSERT t (a) VALUES (1)",
		"INSERT LOW_PRIORITY INTO t VALUES (1)",
		"INSERT IGNORE INTO t VALUES (1)",
		"REPLACE LOW_PRIORITY INTO t VALUES (1)",
		"REPLACE DELAYED t VALUES (1)",
		// A column named "into" in a statement that omits the INTO keyword:
		// scanning for INTO anywhere reads the column list as the target table.
		"INSERT t (`into`) VALUES (1)",
		"REPLACE t (`into`) VALUES (1)",
		"INSERT INTO t VALUES (1), (2)",
		"SELECT REPLACE(name, 'a', 'b') FROM t",
		"SELECT * FROM t WHERE a = 'REPLACE INTO x VALUES (1)'",
		"SELECT * FROM users",
		"SELECT id FROM users WHERE id = 1",
		"SELECT id FROM users ORDER BY id",
		"SELECT DISTINCT id FROM users LIMIT 10",
		"SELECT 1",
		"DELETE FROM t",
		"DELETE FROM t WHERE id = 1",
		"UPDATE t SET a = 1",
		"UPDATE t SET a = 1 WHERE id = 2",
		"ALTER TABLE t ADD COLUMN c INT NOT NULL",
		"CREATE TABLE t (id INT)",
		"DROP TABLE t",
		"TRUNCATE TABLE t",
		"BEGIN",
		"COMMIT",
		"SET autocommit = 1",
		"SELECT a FROM t LIMIT 5000, 10",
		"SELECT a FROM t, u",
		"SELECT a FROM t WHERE a IN (1, 2, 3)",
		"SELECT a FROM t WHERE name LIKE '%abc%'",
		"SELECT `a` FROM `t`",
		"(SELECT a FROM t) UNION (SELECT b FROM u)",
	}

	fallback := analyzer.Default()
	exact := analyzer.Default().WithParser(New())
	coreKnows := fallbackKnowsInsertLikeKeywords()

	for _, sql := range corpus {
		t.Run(sql, func(t *testing.T) {
			if !coreKnows && needsCoreKeywordSupport(sql) {
				t.Skip("linked core predates the fallback's row-inserting keyword support")
			}
			base := ruleSet(fallback.Analyze(sql))
			for name := range ruleSet(exact.Analyze(sql)) {
				if _, ok := base[name]; !ok {
					t.Errorf("%s reports %q, the fallback does not", "mysqlparser", name)
				}
			}
		})
	}
}

// needsCoreKeywordSupport reports whether a corpus entry's parity depends on
// the core recognizing a row-inserting keyword other than a leading
// "INSERT INTO". It matches on the keyword appearing anywhere, not just at the
// front, because a CTE prefix puts it mid-statement; an entry that merely
// mentions REPLACE as a function is skipped too, which costs nothing.
func needsCoreKeywordSupport(sql string) bool {
	up := strings.ToUpper(strings.TrimSpace(sql))
	if strings.Contains(up, "REPLACE") || strings.Contains(up, "UPSERT") {
		return true
	}
	// INSERT with the optional INTO omitted.
	i := strings.Index(up, "INSERT")
	return i >= 0 && !strings.HasPrefix(strings.TrimSpace(up[i+len("INSERT"):]), "INTO")
}

func ruleSet(rs []analyzer.Result) map[string]struct{} {
	out := make(map[string]struct{}, len(rs))
	for _, r := range rs {
		out[r.RuleName] = struct{}{}
	}
	return out
}

// fallbackKnowsInsertLikeKeywords reports whether the linked core recognizes
// the row-inserting keywords that are not spelled INSERT. That support and
// this test ship in the same release, but this is a separate module: under
// GOWORK=off it builds against the core its go.mod requires, which until the
// next lockstep tag predates the support. Parity against an older core is a
// version skew rather than a parity bug, so the rows that depend on it are
// skipped there instead of failing. go.work (local dev and CI) always builds
// against this tree, where they run.
func fallbackKnowsInsertLikeKeywords() bool {
	st, _ := analyzer.NewFallbackParser().Parse("REPLACE INTO t VALUES (1)")
	return st.Kind == analyzer.StmtInsert
}
