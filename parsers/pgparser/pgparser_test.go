package pgparser

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
			// DEFAULT VALUES names no columns and needs none: it inserts no
			// data, so there is no column order for a schema change to shift.
			name: "insert default values counts as listed",
			sql:  "INSERT INTO users DEFAULT VALUES",
			want: analyzer.Statement{Kind: analyzer.StmtInsert, InsertColumnsListed: true, Exact: true},
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

// TestParser_NeverAddsFindingTheFallbackDoesNot pins the direction of the
// trade-off website/docs/parsers.md promises: opting into the real grammar
// removes findings the heuristics guessed wrong, and never adds one the
// zero-dependency default would not have produced. A statement kind the
// grammar understands but the rules were never taught about is the shape that
// breaks it — INSERT ... DEFAULT VALUES did, by refilling
// InsertColumnsListed from len(Columns) alone after blanking it (#68).
//
// Some rows below need matching support in the core's fallback parser; see
// fallbackKnowsInsertLikeKeywords for why they are skipped against an older
// core rather than failing.
func TestParser_NeverAddsFindingTheFallbackDoesNot(t *testing.T) {
	// Fallback-only findings are expected and allowed: they are the false
	// positives the grammar exists to drop.
	corpus := []string{
		"INSERT INTO t DEFAULT VALUES",
		"INSERT INTO t DEFAULT VALUES RETURNING id",
		// UPSERT is accepted by the CockroachDB-derived grammar behind this
		// parser, so it reaches the INSERT node while a keyword-list heuristic
		// would not recognize the statement at all.
		"UPSERT INTO t VALUES (1)",
		"UPSERT INTO t (a) VALUES (1)",
		// A CTE prefix puts the keyword mid-statement, past the leading-keyword
		// check that classifies the plain form.
		"WITH c AS (SELECT 1 AS n) UPSERT INTO t SELECT n FROM c",
		"WITH c AS (SELECT 1 AS n) UPSERT INTO t (a) SELECT n FROM c",
		"WITH c AS (SELECT replace(a, 'x', 'y') AS n FROM u) SELECT n FROM c",
		"INSERT INTO t VALUES (1) RETURNING id",
		"INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO NOTHING",
		"SELECT replace(name, 'a', 'b') FROM t",
		"INSERT INTO t (a, b) VALUES (1, 2)",
		"INSERT INTO t VALUES (1, 2)",
		"INSERT INTO t (a) SELECT x FROM u",
		"WITH c AS (SELECT 1) INSERT INTO t (a) SELECT n FROM c",
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
		"ALTER TABLE t ADD COLUMN c INT NOT NULL DEFAULT 0",
		"CREATE TABLE t (id INT)",
		"CREATE INDEX idx ON t (a)",
		"DROP TABLE t",
		"TRUNCATE TABLE t",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"SET search_path = public",
		"SELECT a FROM t ORDER BY a OFFSET 5000",
		"SELECT a FROM t, u WHERE t.id = u.id",
		"SELECT a FROM t, u",
		"SELECT a FROM t WHERE a IN (1, 2, 3)",
		"SELECT a FROM t WHERE name LIKE '%abc%'",
		"SELECT a FROM t WHERE lower(email) = 'x'",
		"(SELECT a FROM t) UNION (SELECT b FROM u)",
		"VALUES (1), (2)",
		"SELECT t.* FROM t",
		"SELECT * FROM t LIMIT 1 OFFSET 2000",
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
					t.Errorf("%s reports %q, the fallback does not", "pgparser", name)
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
