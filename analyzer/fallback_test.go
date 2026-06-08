package analyzer

import "testing"

// These cover the exact false-positive classes the production-grade review
// flagged for the old raw-regex engine: comments, keyword-like identifiers,
// CTEs, subqueries, multi-statement input, and driver placeholders. The
// fallback parser must not misfire and must never panic or error.

func TestFallback_CommentsDoNotTriggerRules(t *testing.T) {
	a := Default()
	tests := []struct {
		name  string
		query string
	}{
		{"line comment with DELETE", "SELECT id FROM users WHERE id = 1 -- DELETE FROM users everything"},
		{"block comment with WHERE", "DELETE FROM users /* no WHERE here on purpose */ WHERE id = 1"},
		{"line comment hiding where", "UPDATE users SET active = false WHERE id = 1 -- WHERE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, r := range a.Analyze(tt.query) {
				if r.RuleName == "delete-without-where" || r.RuleName == "update-without-where" {
					t.Errorf("%s: unexpected %s on commented query: %s", tt.name, r.RuleName, tt.query)
				}
			}
		})
	}
}

func TestFallback_CommentedOutClausesAreNotCounted(t *testing.T) {
	a := New(CheckDeleteWithoutWhere)
	// The only WHERE is inside a comment, so this DELETE is genuinely unsafe.
	got := a.Analyze("DELETE FROM users -- WHERE id = 1")
	if len(got) != 1 || got[0].RuleName != "delete-without-where" {
		t.Errorf("expected delete-without-where when WHERE is only in a comment, got %+v", got)
	}
}

func TestFallback_KeywordLikeIdentifiers(t *testing.T) {
	a := Default()
	// Column/table names containing keyword substrings must not be parsed
	// as clauses.
	queries := []string{
		"SELECT id, update_at, where_clause FROM orders WHERE id = 1 LIMIT 1",
		"SELECT limited, ordered_by FROM report WHERE k = 1 LIMIT 10",
		"UPDATE wherehouse SET stock = 0 WHERE id = 7",
	}
	for _, q := range queries {
		results := a.Analyze(q)
		for _, r := range results {
			if r.RuleName == "update-without-where" || r.RuleName == "delete-without-where" {
				t.Errorf("keyword-like identifier misparsed in %q: got %s", q, r.RuleName)
			}
		}
	}
}

func TestFallback_CTEAndSubquery(t *testing.T) {
	p := NewFallbackParser()

	st, err := p.Parse("WITH recent AS (SELECT id FROM orders WHERE ts > now()) DELETE FROM orders WHERE id IN (SELECT id FROM recent)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Kind != StmtDelete {
		t.Errorf("CTE-wrapped DELETE: got kind %v, want StmtDelete", st.Kind)
	}
	if !st.HasWhere {
		t.Error("CTE-wrapped DELETE: WHERE clause not detected")
	}

	st, _ = p.Parse("WITH t AS (SELECT 1) SELECT id FROM t WHERE id = 1")
	if st.Kind != StmtSelect {
		t.Errorf("CTE SELECT: got kind %v, want StmtSelect", st.Kind)
	}
}

func TestFallback_PlaceholdersNeverErrorOrPanic(t *testing.T) {
	p := NewFallbackParser()
	queries := []string{
		"SELECT * FROM users WHERE id = $1",
		"SELECT * FROM users WHERE id = ? AND name = ?",
		"SELECT * FROM users WHERE id = :id",
		"INSERT INTO t VALUES ($1, $2); DELETE FROM other",
		"",
		"not even sql",
		"SELECT '%' || $1 || '%'",
	}
	for _, q := range queries {
		st, err := p.Parse(q)
		if err != nil {
			t.Errorf("fallback returned error for %q: %v (it must never error)", q, err)
		}
		if st == nil {
			t.Errorf("fallback returned nil Statement for %q", q)
			continue
		}
		if st.Exact {
			t.Errorf("fallback Statement for %q must have Exact=false", q)
		}
	}
}

func TestFallback_MultiStatementLeadingKind(t *testing.T) {
	p := NewFallbackParser()
	st, _ := p.Parse("DELETE FROM a WHERE id = 1; DROP TABLE b")
	if st.Kind != StmtDelete {
		t.Errorf("multi-statement: got kind %v, want StmtDelete (leading statement)", st.Kind)
	}
}
