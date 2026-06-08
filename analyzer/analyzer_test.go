package analyzer

import "testing"

// run parses q with the fallback parser and applies a single rule, returning
// whether the rule fired. Rules operate on a parsed Statement now, so tests
// go through the parser the same way Analyze does.
func run(t *testing.T, rule Rule, q string) bool {
	t.Helper()
	st, err := NewFallbackParser().Parse(q)
	if err != nil {
		t.Fatalf("fallback parser returned error for %q: %v", q, err)
	}
	_, ok := rule(st)
	return ok
}

func TestCheckSelectStar(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"basic select star", "SELECT * FROM users", true},
		{"lowercase", "select * from users", true},
		{"with where", "SELECT * FROM users WHERE id = 1", true},
		{"qualified star", "SELECT u.* FROM users u", true},
		{"specific columns", "SELECT id, name FROM users", false},
		{"count star", "SELECT COUNT(*) FROM users", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckSelectStar, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckLeadingWildcard(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"leading wildcard", "SELECT * FROM users WHERE email LIKE '%gmail.com%'", true},
		{"trailing only", "SELECT * FROM users WHERE name LIKE 'John%'", false},
		{"double quotes", `SELECT * FROM users WHERE email LIKE "%gmail%"`, true},
		{"ilike leading wildcard", "SELECT * FROM users WHERE email ILIKE '%gmail%'", true},
		{"ilike trailing only", "SELECT * FROM users WHERE name ILIKE 'John%'", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckLeadingWildcard, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckDeleteWithoutWhere(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"no where", "DELETE FROM users", true},
		{"with where", "DELETE FROM users WHERE id = 1", false},
		{"not a delete", "SELECT * FROM users", false},
		{"where in string literal", "DELETE FROM logs WHERE msg = 'no WHERE clause'", false},
		{"fake where in string", "DELETE FROM users SET bio = 'I live WHERE the sun shines'", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckDeleteWithoutWhere, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckUpdateWithoutWhere(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"no where", "UPDATE users SET name = 'test'", true},
		{"with where", "UPDATE users SET name = 'test' WHERE id = 1", false},
		{"not an update", "SELECT * FROM users", false},
		{"where in string literal", "UPDATE users SET bio = 'I live WHERE the sun shines'", true},
		{"real where after string", "UPDATE users SET bio = 'hello' WHERE id = 1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckUpdateWithoutWhere, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckInsertWithoutColumns(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"no columns", "INSERT INTO users VALUES ('alice', 'alice@test.com')", true},
		{"with columns", "INSERT INTO users (name, email) VALUES ('alice', 'alice@test.com')", false},
		{"not an insert", "SELECT * FROM users", false},
		{"insert select no columns", "INSERT INTO users SELECT name, email FROM staging", true},
		{"insert select with columns", "INSERT INTO users (name, email) SELECT name, email FROM staging", false},
		{"qualified table no columns", "INSERT INTO public.users VALUES ('alice')", true},
		{"mysql set form", "INSERT INTO users SET name = 'alice', email = 'a@test.com'", false},
		{"default values", "INSERT INTO users DEFAULT VALUES", false},
		{"cte insert no columns", "WITH s AS (SELECT 1) INSERT INTO users SELECT * FROM s", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckInsertWithoutColumns, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckSelectWithoutLimit(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"no limit no where", "SELECT id FROM users", true},
		{"with limit", "SELECT id FROM users LIMIT 10", false},
		{"with where", "SELECT id FROM users WHERE id = 1", false},
		{"with both", "SELECT id FROM users WHERE id > 0 LIMIT 10", false},
		{"not a select", "DELETE FROM users", false},
		{"select without from", "SELECT 1", false},
		{"select version", "SELECT version()", false},
		{"select current_timestamp", "SELECT CURRENT_TIMESTAMP", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckSelectWithoutLimit, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckOrderByWithoutLimit(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"order without limit", "SELECT id FROM users ORDER BY name", true},
		{"order with limit", "SELECT id FROM users ORDER BY name LIMIT 10", false},
		{"no order by", "SELECT id FROM users", false},
		{"window order by", "SELECT row_number() OVER (ORDER BY id) FROM users", false},
		{"ordered aggregate", "SELECT GROUP_CONCAT(x ORDER BY y) FROM t", false},
		{"window order by with top-level order by", "SELECT rank() OVER (ORDER BY a) FROM t ORDER BY b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckOrderByWithoutLimit, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckNonSargablePredicate(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"lower on column", "SELECT id FROM users WHERE LOWER(email) = 'x'", true},
		{"date on column", "SELECT id FROM events WHERE DATE(created_at) = '2020-01-01'", true},
		{"cast on column", "SELECT id FROM users WHERE CAST(id AS text) = '5'", true},
		{"coalesce on column", "SELECT id FROM users WHERE COALESCE(deleted, false) = false", true},
		{"like on wrapped column", "SELECT id FROM users WHERE UPPER(name) LIKE 'A%'", true},
		{"function on value side", "SELECT id FROM users WHERE email = LOWER('X')", false},
		{"now on value side", "SELECT id FROM events WHERE created_at > NOW()", false},
		{"bare column", "SELECT id FROM users WHERE email = 'x'", false},
		{"in list not a function", "SELECT id FROM users WHERE id IN (1, 2, 3)", false},
		{"function in select list", "SELECT LOWER(name) FROM users", false},
		{"function in order by", "SELECT id FROM users WHERE active = true ORDER BY LOWER(name)", false},
		{"commented out predicate", "SELECT id FROM users -- WHERE LOWER(email) = 'x'", false},
		{"predicate after subquery clause", "SELECT id FROM users WHERE id IN (SELECT uid FROM o ORDER BY x LIMIT 1) AND LOWER(name) = 'a'", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckNonSargablePredicate, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckAddNotNullWithoutDefault(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"add not null no default", "ALTER TABLE users ADD COLUMN age int NOT NULL", true},
		{"add not null without column kw", "ALTER TABLE users ADD age int NOT NULL", true},
		{"numeric type with comma", "ALTER TABLE t ADD COLUMN bal numeric(10,2) NOT NULL", true},
		{"multi action one unsafe", "ALTER TABLE t ADD COLUMN a int NOT NULL, ADD COLUMN b int DEFAULT 5", true},
		{"not null with default", "ALTER TABLE users ADD COLUMN age int NOT NULL DEFAULT 0", false},
		{"default before not null", "ALTER TABLE users ADD COLUMN age int DEFAULT 0 NOT NULL", false},
		{"nullable column", "ALTER TABLE users ADD COLUMN age int", false},
		{"set not null on existing", "ALTER TABLE users ALTER COLUMN age SET NOT NULL", false},
		{"add check constraint is not null", "ALTER TABLE users ADD CONSTRAINT chk CHECK (age IS NOT NULL)", false},
		{"not an alter", "INSERT INTO users (age) VALUES (1)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckAddNotNullWithoutDefault, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckImplicitJoin(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"two table comma join", "SELECT * FROM a, b WHERE a.id = b.id", true},
		{"three tables", "SELECT * FROM a, b, c WHERE a.id = b.id AND b.id = c.id", true},
		{"comma plus explicit join", "SELECT * FROM a, b JOIN c ON b.id = c.id", true},
		{"explicit join only", "SELECT * FROM a JOIN b ON a.id = b.id", false},
		{"single table", "SELECT * FROM users WHERE id = 1", false},
		{"select list comma not from", "SELECT id, name FROM users", false},
		{"comma inside in list", "SELECT * FROM users WHERE id IN (1, 2, 3)", false},
		{"comma inside function", "SELECT * FROM generate_series(1, 10)", false},
		{"comma inside subquery", "SELECT * FROM (SELECT a, b FROM t) sub", false},
		{"from inside extract", "SELECT EXTRACT(YEAR FROM created_at) FROM events", false},
		{"extract then comma join", "SELECT EXTRACT(YEAR FROM ts) FROM events, logs WHERE events.id = logs.id", true},
		{"no from", "SELECT 1, 2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckImplicitJoin, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckCartesianJoin(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"comma join no where", "SELECT * FROM a, b", true},
		{"three tables no where", "SELECT * FROM a, b, c", true},
		{"explicit cross join", "SELECT * FROM a CROSS JOIN b", true},
		{"cross join with where", "SELECT * FROM a CROSS JOIN b WHERE a.x = 1", false},
		{"comma join with where", "SELECT * FROM a, b WHERE a.id = b.id", false},
		{"join with on", "SELECT * FROM a JOIN b ON a.id = b.id", false},
		{"join with using", "SELECT * FROM a JOIN b USING (id)", false},
		{"natural join", "SELECT * FROM a NATURAL JOIN b", false},
		{"single table", "SELECT * FROM users", false},
		{"subquery cross product", "SELECT * FROM (SELECT * FROM x WHERE y = 1) sub, t", true},
		{"cross join only in subquery", "SELECT x FROM (SELECT * FROM a CROSS JOIN b) sub", false},
		{"conditioned join only in subquery", "SELECT x FROM (SELECT * FROM a JOIN b ON a.id = b.id) sub", false},
		{"no from", "SELECT 1, 2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckCartesianJoin, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckInListTooLarge(t *testing.T) {
	rule := inListRule(5) // flag IN lists with more than 5 elements
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"over threshold", "SELECT * FROM t WHERE id IN (1, 2, 3, 4, 5, 6)", true},
		{"at threshold", "SELECT * FROM t WHERE id IN (1, 2, 3, 4, 5)", false},
		{"under threshold", "SELECT * FROM t WHERE id IN (1, 2, 3)", false},
		{"not in over threshold", "SELECT * FROM t WHERE id NOT IN (1, 2, 3, 4, 5, 6)", true},
		{"placeholders over threshold", "SELECT * FROM t WHERE id IN (?, ?, ?, ?, ?, ?)", true},
		{"string literals over threshold", "SELECT * FROM t WHERE c IN ('a', 'b', 'c', 'd', 'e', 'f')", true},
		{"subquery not counted", "SELECT * FROM t WHERE id IN (SELECT id FROM other)", false},
		{"no in list", "SELECT * FROM t WHERE id = 5", false},
		{"function commas not an in list", "SELECT * FROM t WHERE x = greatest(1, 2, 3, 4, 5, 6, 7)", false},
		{"largest of multiple lists", "SELECT * FROM t WHERE a IN (1, 2) AND b IN (1, 2, 3, 4, 5, 6, 7)", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, rule, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckLargeOffset(t *testing.T) {
	rule := largeOffsetRule(1000)
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"large offset", "SELECT * FROM t ORDER BY id LIMIT 20 OFFSET 5000", true},
		{"at threshold", "SELECT * FROM t ORDER BY id LIMIT 20 OFFSET 1000", false},
		{"small offset", "SELECT * FROM t ORDER BY id LIMIT 20 OFFSET 40", false},
		{"no offset", "SELECT * FROM t ORDER BY id LIMIT 20", false},
		{"parameterized offset", "SELECT * FROM t ORDER BY id LIMIT 20 OFFSET $1", false},
		{"offset rows fetch", "SELECT * FROM t ORDER BY id OFFSET 5000 ROWS FETCH NEXT 20 ROWS ONLY", true},
		{"mysql limit offset comma", "SELECT * FROM t ORDER BY id LIMIT 5000, 20", true},
		{"mysql limit small offset", "SELECT * FROM t ORDER BY id LIMIT 40, 20", false},
		{"offset as column name", "SELECT offset FROM t WHERE offset = 5000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, rule, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestCheckSelectDistinct(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantHit bool
	}{
		{"basic distinct", "SELECT DISTINCT name FROM users", true},
		{"lowercase", "select distinct id from t", true},
		{"distinct on postgres", "SELECT DISTINCT ON (dept) name FROM emp", true},
		{"distinct parens", "SELECT DISTINCT(name) FROM users", true},
		{"distinctrow mysql", "SELECT DISTINCTROW name FROM users", true},
		{"distinct in subquery", "SELECT * FROM (SELECT DISTINCT x FROM t) s", true},
		{"no distinct", "SELECT name FROM users", false},
		{"count distinct aggregate", "SELECT COUNT(DISTINCT name) FROM users", false},
		{"distinct in aggregate with group", "SELECT id, COUNT(DISTINCT x) FROM t GROUP BY id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, CheckSelectDistinct, tt.query); got != tt.wantHit {
				t.Errorf("got hit=%v, want %v for query: %s", got, tt.wantHit, tt.query)
			}
		})
	}
}

func TestDefaultAnalyzer(t *testing.T) {
	a := Default()

	results := a.Analyze("DELETE FROM users")
	if len(results) == 0 {
		t.Fatal("expected at least one result for DELETE without WHERE")
	}
	if results[0].Severity != SeverityCritical {
		t.Errorf("expected critical severity, got %s", results[0].Severity)
	}

	results = a.Analyze("SELECT id FROM users WHERE id = 1")
	if len(results) != 0 {
		t.Errorf("expected no results for safe query, got %d", len(results))
	}
}

// TestSpecDefaultSeverityIsAuthoritative locks in that a registry-built rule's
// reported severity comes from its RuleSpec.DefaultSeverity — the single
// source of truth — not from a literal in the rule body. The rule here
// deliberately returns the zero severity (Info); Analyze must report Critical.
func TestSpecDefaultSeverityIsAuthoritative(t *testing.T) {
	const name = "zz-spec-severity-probe"
	Register(RuleSpec{
		Name:            name,
		DefaultSeverity: SeverityCritical,
		Factory: func(Settings) Rule {
			return func(*Statement) (Result, bool) {
				return Result{RuleName: name}, true // no Severity set
			}
		},
	})
	// Don't leak the probe into the global registry; Default() would pick it up.
	t.Cleanup(func() {
		registryMu.Lock()
		delete(registry, name)
		registryMu.Unlock()
	})

	a := DefaultWithProfile(Profile{Only: map[string]bool{name: true}})
	got := a.Analyze("SELECT 1")
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Severity != SeverityCritical {
		t.Errorf("severity = %s, want CRITICAL (from spec DefaultSeverity)", got[0].Severity)
	}

	// A profile override still wins over the spec default.
	a = DefaultWithProfile(Profile{
		Only:     map[string]bool{name: true},
		Severity: map[string]Severity{name: SeverityInfo},
	})
	if got := a.Analyze("SELECT 1"); len(got) != 1 || got[0].Severity != SeverityInfo {
		t.Errorf("profile override not applied: %+v", got)
	}
}
