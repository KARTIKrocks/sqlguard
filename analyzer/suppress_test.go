package analyzer

import (
	"maps"
	"slices"
	"testing"
)

// TestParseIgnoreDirective: a directive counts only in a comment, never in a
// literal, even one whose end the dialects disagree about (#66).
func TestParseIgnoreDirective(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantAll   bool
		wantRules []string
	}{
		// Genuine directives.
		{"line comment", "SELECT * FROM t -- sqlguard:ignore", true, nil},
		{"block comment scoped", "SELECT * FROM t /* sqlguard:ignore:select-star */", false, []string{"select-star"}},
		{"hash comment", "SELECT * FROM t # sqlguard:ignore", true, nil},
		{"case-insensitive", "SELECT * FROM t -- SQLGuard:Ignore", true, nil},
		{"list with spaces", "SELECT * FROM t -- sqlguard:ignore:select-star, orderby-without-limit", false, []string{"orderby-without-limit", "select-star"}},
		{"after a literal", "SELECT * FROM t WHERE a = 'x' -- sqlguard:ignore", true, nil},
		{"after a literal holding a marker", "SELECT * FROM t WHERE a = '--' -- sqlguard:ignore:select-star", false, []string{"select-star"}},
		{"unterminated block comment", "SELECT * FROM t /* sqlguard:ignore", true, nil},
		{"backslash in an unambiguous literal", `SELECT * FROM t WHERE p = 'C:\dir' -- sqlguard:ignore`, true, nil},
		{"dollar in a literal", "SELECT * FROM t WHERE a = '$$' -- sqlguard:ignore", true, nil},
		{"bind placeholder", "SELECT * FROM t WHERE a = $1 -- sqlguard:ignore", true, nil},

		// Inside a literal: nothing is suppressed.
		{"single-quoted line marker", "SELECT * FROM t WHERE note = '-- sqlguard:ignore'", false, nil},
		{"single-quoted block marker", "SELECT * FROM t WHERE note = '/* sqlguard:ignore */'", false, nil},
		{"single-quoted hash marker", "SELECT * FROM t WHERE note = '# sqlguard:ignore'", false, nil},
		{"bare token in a literal", "SELECT * FROM t WHERE note = 'sqlguard:ignore'", false, nil},
		{"doubled-quote escape", "SELECT * FROM t WHERE note = 'it''s -- sqlguard:ignore'", false, nil},
		{"double-quoted", `SELECT * FROM t WHERE note = "-- sqlguard:ignore"`, false, nil},
		{"backtick identifier", "SELECT `-- sqlguard:ignore` FROM t", false, nil},
		{"dollar-quoted", "SELECT * FROM t WHERE note = $$-- sqlguard:ignore$$", false, nil},
		{"tagged dollar-quoted", "SELECT * FROM t WHERE note = $x$ /* sqlguard:ignore */ $x$", false, nil},
		{"unterminated literal", "SELECT * FROM t WHERE note = 'abc -- sqlguard:ignore", false, nil},

		// Dialect-ambiguous: only one reading puts the marker in a comment.
		{"backslash-escaped quote", `SELECT * FROM t WHERE note = 'x\' -- sqlguard:ignore'`, false, nil},
		{"dollar reading", "SELECT * FROM t WHERE a = $$'$$ -- sqlguard:ignore'", false, nil},

		// Markers only some dialects accept: a genuine comment still counts,
		// but a marker that is an operator elsewhere cannot reach into a literal.
		{"hash before a literal", "SELECT * FROM t WHERE flags # 4 = 0 AND note = 'sqlguard:ignore'", false, nil},
		{"hash before a marker literal", "SELECT * FROM t WHERE flags # 4 = 0 AND note = '-- sqlguard:ignore'", false, nil},
		{"mysql double minus", "SELECT * FROM t WHERE a = 1--'-- sqlguard:ignore'", false, nil},
		{"tight double dash", "SELECT * FROM t --sqlguard:ignore", true, nil},
		// MySQL's own mix: # is a comment, --x is not, so a string opens after it.
		{"mysql marker mix", "SELECT * FROM t WHERE a = 1 # '\n--x' -- sqlguard:ignore '", false, nil},

		// "//" is not a SQL comment in any dialect.
		{"double slash", "SELECT * FROM t // sqlguard:ignore", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all, rules := parseIgnoreDirective(tt.sql)
			got := slices.Sorted(maps.Keys(rules))
			if all != tt.wantAll || !slices.Equal(got, tt.wantRules) {
				t.Errorf("parseIgnoreDirective(%q) = (%v, %v), want (%v, %v)", tt.sql, all, got, tt.wantAll, tt.wantRules)
			}
		})
	}
}

// TestIgnoreDirectiveInLiteralDoesNotSuppress is the #66 reproduction.
func TestIgnoreDirectiveInLiteralDoesNotSuppress(t *testing.T) {
	q := "SELECT * FROM users WHERE note = '-- sqlguard:ignore'"
	if got := filterByRule(Default().Analyze(q), "select-star"); got != 1 {
		t.Errorf("select-star reported %d times on %q, want 1", got, q)
	}
}
