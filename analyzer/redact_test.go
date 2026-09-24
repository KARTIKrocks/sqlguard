package analyzer

import "testing"

func TestRedact(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"string literal", `SELECT * FROM users WHERE email = 'alice@acme.com'`,
			`SELECT * FROM users WHERE email = ?`},
		{"numeric literal", `SELECT * FROM t WHERE id = 42 AND age > 18`,
			`SELECT * FROM t WHERE id = ? AND age > ?`},
		{"float and exponent", `SELECT * FROM t WHERE x = 3.14 AND y = 1e10`,
			`SELECT * FROM t WHERE x = ? AND y = ?`},
		{"identifier with digits kept", `SELECT col1, int8_v FROM t1 WHERE a2 = 5`,
			`SELECT col1, int8_v FROM t1 WHERE a2 = ?`},
		{"bind placeholders kept", `SELECT * FROM t WHERE a = $1 AND b = @p2`,
			`SELECT * FROM t WHERE a = $1 AND b = @p2`},
		{"quoted identifier preserved", `SELECT "weird;col" FROM t WHERE n = 'x'`,
			`SELECT "weird;col" FROM t WHERE n = ?`},
		{"backtick identifier preserved", "SELECT `from` FROM t WHERE n = 'x'",
			"SELECT `from` FROM t WHERE n = ?"},
		{"escaped quote in literal", `SELECT * FROM t WHERE s = 'O''Brien'`,
			`SELECT * FROM t WHERE s = ?`},
		{"backslash-escaped quote", `SELECT * FROM t WHERE s = 'O\'Brien'`,
			`SELECT * FROM t WHERE s = ?`},
		{"even backslashes end the literal", `SELECT * FROM t WHERE a = 'x\\' AND b = 'y'`,
			`SELECT * FROM t WHERE a = ? AND b = ?`},
		{"dollar-quoted string", `SELECT * FROM t WHERE body = $$a 'b' c$$`,
			`SELECT * FROM t WHERE body = ?`},
		{"tagged dollar-quoted string", `SELECT * FROM t WHERE body = $fn$x$fn$`,
			`SELECT * FROM t WHERE body = ?`},
		{"hex literal", `SELECT * FROM t WHERE h = 0xDEADBEEF`,
			`SELECT * FROM t WHERE h = ?`},
		{"binary literal", `SELECT * FROM t WHERE b = 0b1011`,
			`SELECT * FROM t WHERE b = ?`},
		{"lone dollars are not a quote", `SELECT x$$y FROM t WHERE id = 3`,
			`SELECT x$$y FROM t WHERE id = ?`},
		// Postgres allows "$" inside an identifier after its first character,
		// so these dollars continue the name rather than opening a literal.
		{"dollars inside an identifier are not a quote",
			`SELECT foo$tag$value$tag$ FROM t WHERE id = 3`,
			`SELECT foo$tag$value$tag$ FROM t WHERE id = ?`},
		{"real comments still stripped around a dollar body",
			`SELECT $$body$$ /* note */ FROM t`,
			`SELECT ?   FROM t`},
		{"dollar signs in a comment do not open a quote",
			"SELECT a -- $$ x\nFROM t WHERE id = 1",
			"SELECT a  \nFROM t WHERE id = ?"},
		{"comment stripped", "SELECT a -- secret 'tok'\nFROM t WHERE id = 9",
			"SELECT a  \nFROM t WHERE id = ?"},
		{"semicolon inside literal not structural", `SELECT * FROM t WHERE s = 'a;b'`,
			`SELECT * FROM t WHERE s = ?`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Redact(c.in); got != c.want {
				t.Errorf("Redact(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactNoPII(t *testing.T) {
	pii := []string{"alice@acme.com", "123-45-6789", "4111111111111111", "secret"}
	q := `SELECT * FROM users WHERE email='alice@acme.com' AND ssn='123-45-6789'
	      AND card='4111111111111111' /* secret */ LIMIT 10`
	got := Redact(q)
	for _, p := range pii {
		if contains(got, p) {
			t.Errorf("Redact leaked %q: %q", p, got)
		}
	}
}

// TestRedactNoLeakAcrossDialectAmbiguity pins the security invariant from
// SECURITY.md: no literal byte survives Redact (and therefore Fingerprint),
// whichever dialect reading of a backslash escape is correct. Each of these
// desynchronised the old single-pass lexer, which closed a literal at the
// wrong quote and then emitted the following literal's contents verbatim.
func TestRedactNoLeakAcrossDialectAmbiguity(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		secrets []string
	}{
		{"mysql backslash escape",
			`SELECT * FROM u WHERE name = 'O\'Brien' AND ssn = '123-45-6789'`,
			[]string{"Brien", "123-45-6789"}},
		{"postgres E-string escape",
			`SELECT * FROM u WHERE a = E'it\'s' AND token = 'sk-live-abcdef'`,
			[]string{"sk-live-abcdef"}},
		{"trailing backslash under standard_conforming_strings",
			`SELECT * FROM t WHERE path = 'C:\' AND secret = 'hunter2'`,
			[]string{"hunter2"}},
		{"escape ambiguity across a comment",
			"SELECT * FROM t WHERE s = 'a\\' /* x */ AND tok = 'ghp_deadbeef'",
			[]string{"ghp_deadbeef"}},
		{"dollar-quoted body",
			`SELECT * FROM u WHERE bio = $$super secret value$$`,
			[]string{"super secret value"}},
		{"tagged dollar-quoted body",
			`SELECT * FROM u WHERE bio = $tag$another secret$tag$`,
			[]string{"another secret"}},
		{"quote inside dollar-quoted body",
			`SELECT $$it's 'nested'$$, email FROM u WHERE e = 'a@b.c'`,
			[]string{"nested", "a@b.c"}},
		// A comment marker inside a dollar-quoted body is data. If
		// stripComments treated it as a comment it would eat the closing
		// $tag$ with it, stranding the body outside any literal span and
		// copying it straight out — see stripComments in fallback.go.
		{"line-comment marker inside dollar-quoted body",
			`SELECT * FROM t WHERE a = $$call 555-1234 -- ok$$ AND b = 1`,
			[]string{"call", "555-1234"}},
		{"line-comment marker inside tagged dollar-quoted body",
			`SELECT * FROM t WHERE a = $tag$SECRET -- x$tag$`,
			[]string{"SECRET"}},
		{"block-comment marker inside dollar-quoted body",
			`SELECT * FROM t WHERE a = $$SECRET /* x$$ AND b = 2`,
			[]string{"SECRET"}},
		{"trailing comment after a dollar body holding a quote",
			`SELECT $$a'b$$ -- alice@example.com`,
			[]string{"alice@example.com"}},
		// Postgres dollar-quote tags follow unquoted-identifier rules, which
		// admit non-ASCII letters, so the delimiter scan cannot be ASCII-only.
		{"non-ascii dollar-quote tag",
			`SELECT * FROM u WHERE bio = $é$secret$é$ AND id = 1`,
			[]string{"secret"}},
		{"multibyte dollar-quote tag",
			`SELECT * FROM u WHERE bio = $日本$secret$日本$`,
			[]string{"secret"}},
		// A truncated query leaves the body unterminated; it is still body.
		{"unterminated dollar-quoted body",
			`SELECT * FROM u WHERE bio = $$dangling secret`,
			[]string{"dangling secret"}},
		{"unterminated tagged dollar-quoted body",
			`SELECT * FROM u WHERE bio = $tag$dangling secret`,
			[]string{"dangling secret"}},
		{"hex payload",
			`SELECT * FROM u WHERE x = 0x4142414241424142`,
			[]string{"4142414241424142"}},
		{"unterminated literal",
			`SELECT * FROM u WHERE s = 'dangling secret`,
			[]string{"dangling secret"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, fp := Redact(c.in), Fingerprint(c.in)
			for _, s := range c.secrets {
				if contains(got, s) {
					t.Errorf("Redact leaked %q\n  in:  %s\n  out: %s", s, c.in, got)
				}
				if contains(fp, s) {
					t.Errorf("Fingerprint leaked %q\n  in: %s\n  fp: %s", s, c.in, fp)
				}
			}
		})
	}
}

func TestFingerprint(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"collapse whitespace", "SELECT   *\n FROM   t  WHERE id = 1",
			"SELECT * FROM t WHERE id = ?"},
		{"fold IN list", `SELECT * FROM t WHERE id IN (1, 2, 3, 4)`,
			`SELECT * FROM t WHERE id IN (?)`},
		{"fold VALUES tuple", `INSERT INTO t VALUES ('a', 'b', 'c')`,
			`INSERT INTO t VALUES (?)`},
		{"trailing semicolon trimmed", `SELECT 1;`, `SELECT ?`},
		{"differing literals same fp",
			`SELECT * FROM t WHERE name = 'bob' AND age = 7`,
			`SELECT * FROM t WHERE name = ? AND age = ?`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Fingerprint(c.in); got != c.want {
				t.Errorf("Fingerprint(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}

	// Stability: queries differing only in values/list length share a fp.
	a := Fingerprint(`SELECT * FROM t WHERE id IN (1,2,3) AND s = 'x'`)
	b := Fingerprint(`SELECT * FROM t WHERE id IN (9,8) AND s = 'zzzzz'`)
	if a != b {
		t.Errorf("fingerprints should match:\n a=%q\n b=%q", a, b)
	}
}

func TestIsMultiStatement(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"single", `SELECT * FROM t WHERE id = 1`, false},
		{"trailing semicolon", `SELECT * FROM t;`, false},
		{"trailing semicolon + ws", "SELECT 1;  \n\t", false},
		{"stacked", `SELECT 1; DROP TABLE users`, true},
		{"stacked no space", `SELECT 1;DELETE FROM t`, true},
		{"semicolon in line comment", "SELECT 1 -- a; b\n", false},
		{"semicolon in block comment", `SELECT 1 /* a ; b */`, false},
		{"semicolon in string literal", `SELECT * FROM t WHERE s = 'a; DROP'`, false},
		{"comment hides stacking attempt", "SELECT 1 -- ;\nfrom t", false},
		{"real stack after string", `SELECT 'a;b'; DELETE FROM t`, true},
		// IsMultiStatement takes the narrowest reading of a literal, the
		// opposite of Redact's: if "\'" closes the literal on the target
		// server, the tail really is a second statement, so it must be
		// refused. Over-rejecting a one-statement query is the safe error.
		{"backslash escape must not hide a stacked statement",
			`SELECT 'a\'; DROP TABLE users; --'`, true},
		{"backslash escape must not hide a stacked DELETE",
			`SELECT * FROM t WHERE s = 'x\'; DELETE FROM t; --'`, true},
		// Dollar quotes are Postgres-only syntax, but this check guards
		// explain on every dialect, and MySQL reads "$$" as ordinary
		// identifier bytes. Blanking a dollar body here would erase a real
		// MySQL separator: with --allow-dml the EXPLAIN runs in a read-write
		// transaction, and a smuggled DDL statement implicit-commits, so
		// rollback would not undo it. These stay refused on purpose — a
		// Postgres-only query occasionally rejected is the affordable error.
		{"semicolon in dollar-quoted body", `SELECT $$a ; b$$`, true},
		{"comment marker and semicolon in dollar-quoted body",
			`SELECT $$-- ; note$$`, true},
		{"real stack after a dollar body", `SELECT $$x$$ ; DROP TABLE t`, true},
		{"dollar signs must not hide a mysql separator",
			`UPDATE t AS $$ SET id = 1; DROP TABLE t`, true},
		{"tagged dollar signs must not hide a mysql separator",
			`UPDATE t SET a = 1 $x$ ; DROP TABLE t`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsMultiStatement(c.in); got != c.want {
				t.Errorf("IsMultiStatement(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
