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
