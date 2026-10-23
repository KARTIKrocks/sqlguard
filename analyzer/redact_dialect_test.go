package analyzer

import (
	"strings"
	"testing"
)

// TestRedactDoubleQuotesByDialect pins how a double-quoted run is read: a
// string under MySQL's default sql_mode, an identifier under Postgres, and
// with the dialect unknown an identifier only when it is shaped like one (#62).
func TestRedactDoubleQuotesByDialect(t *testing.T) {
	cases := []struct {
		name, sql          string
		unknown, pg, mysql string
	}{
		{"orm identifiers",
			`SELECT "id" FROM "users" WHERE "users"."email" = 'x'`,
			`SELECT "id" FROM "users" WHERE "users"."email" = ?`,
			`SELECT "id" FROM "users" WHERE "users"."email" = ?`,
			`SELECT ? FROM ? WHERE ?.? = ?`},
		{"email value",
			`SELECT * FROM t WHERE email = "alice@example.com"`,
			`SELECT * FROM t WHERE email = ?`,
			`SELECT * FROM t WHERE email = "alice@example.com"`,
			`SELECT * FROM t WHERE email = ?`},
		{"identifier-shaped value",
			`SELECT * FROM t WHERE name = "alice"`,
			`SELECT * FROM t WHERE name = "alice"`,
			`SELECT * FROM t WHERE name = "alice"`,
			`SELECT * FROM t WHERE name = ?`},
		{"doubled quote",
			`SELECT * FROM t WHERE n = "it""s"`,
			`SELECT * FROM t WHERE n = ?`,
			`SELECT * FROM t WHERE n = "it""s"`,
			`SELECT * FROM t WHERE n = ?`},
		{"unterminated",
			`SELECT * FROM t WHERE n = "secret`,
			`SELECT * FROM t WHERE n = ?`,
			`SELECT * FROM t WHERE n = ?`,
			`SELECT * FROM t WHERE n = ?`},
		{"backticks stay identifiers",
			"SELECT `from` FROM t WHERE n = 1",
			"SELECT `from` FROM t WHERE n = ?",
			"SELECT `from` FROM t WHERE n = ?",
			"SELECT `from` FROM t WHERE n = ?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, w := range []struct {
				d    Dialect
				want string
			}{{DialectUnknown, c.unknown}, {DialectPostgres, c.pg}, {DialectMySQL, c.mysql}} {
				if got := redact(c.sql, w.d); got != w.want {
					t.Errorf("%v:\n got: %q\nwant: %q", w.d, got, w.want)
				}
			}
			if got := Redact(c.sql); got != c.unknown {
				t.Errorf("Redact = %q, want the unknown reading %q", got, c.unknown)
			}
		})
	}
}

// TestRedactMySQLDoubleQuotesNoLeakAcrossBackslash pins that the backslash
// union still covers a MySQL double-quoted literal: whichever reading is right,
// the value never survives.
func TestRedactMySQLDoubleQuotesNoLeakAcrossBackslash(t *testing.T) {
	sql := `SELECT * FROM t WHERE a = "x\" AND b = 'secret' AND c = "y"`
	if got := redact(sql, DialectMySQL); strings.Contains(got, "secret") {
		t.Errorf("leaked: %q", got)
	}
}

// TestAnalyzerUsesItsDialect pins that every surface reading the analyzer —
// Analyze, PrepareQuery and Fingerprint — redacts in its dialect.
// TestRedactPostgresNameEndsAtItsQuote pins that a backslash never escapes the
// closing quote of a Postgres name, which would swallow the E'...' after it.
func TestRedactPostgresNameEndsAtItsQuote(t *testing.T) {
	sql := `SELECT "a\", E'x\' secret' FROM t WHERE "z" = 1`
	if got := redact(sql, DialectPostgres); strings.Contains(got, "secret") {
		t.Errorf("leaked: %q", got)
	}
}

func TestAnalyzerUsesItsDialect(t *testing.T) {
	const sql = `SELECT * FROM t WHERE name = "alice"`
	a := Default().WithDialect(DialectMySQL)
	display, fp := a.PrepareQuery(sql)
	for _, s := range []string{display, fp, a.Fingerprint(sql)} {
		if strings.Contains(s, "alice") {
			t.Errorf("value survived: %q", s)
		}
	}
	if fp != a.Fingerprint(sql) {
		t.Errorf("PrepareQuery fingerprint %q != Fingerprint %q", fp, a.Fingerprint(sql))
	}
	for _, r := range a.Analyze(sql) {
		if strings.Contains(r.Query, "alice") || strings.Contains(r.Fingerprint, "alice") {
			t.Errorf("%s leaked: %q / %q", r.RuleName, r.Query, r.Fingerprint)
		}
		// Guard feeds this to the N+1 tracker in place of Fingerprint.
		if r.Fingerprint != a.Fingerprint(sql) {
			t.Errorf("%s fingerprint %q != Fingerprint %q", r.RuleName, r.Fingerprint, a.Fingerprint(sql))
		}
	}
	if len(a.Analyze(sql)) == 0 {
		t.Fatal("expected select-star to fire so Analyze's redaction is exercised")
	}
}
