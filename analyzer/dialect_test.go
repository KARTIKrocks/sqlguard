package analyzer

import "testing"

func TestParseDialect(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Dialect
		ok   bool
	}{
		{"postgres", DialectPostgres, true},
		{" PostgreSQL ", DialectPostgres, true},
		{"pgx", DialectPostgres, true},
		{"mysql", DialectMySQL, true},
		{"MariaDB", DialectMySQL, true},
		{"sqlite3", DialectUnknown, false},
		{"unknown", DialectUnknown, false},
		{"", DialectUnknown, false},
	} {
		got, ok := ParseDialect(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseDialect(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	for _, d := range []Dialect{DialectPostgres, DialectMySQL} {
		if got, _ := ParseDialect(d.String()); got != d {
			t.Errorf("ParseDialect(%v.String()) = %v", d, got)
		}
	}
}

type pgOnlyParser struct{ FallbackParser }

func (*pgOnlyParser) Dialect() Dialect { return DialectPostgres }

// TestAnalyzerDialect pins how the dialect is set: a single-dialect parser
// fills it only while it is unknown, and WithDialect always replaces it.
func TestAnalyzerDialect(t *testing.T) {
	if d := Default().Dialect(); d != DialectUnknown {
		t.Errorf("Default dialect = %v, want unknown", d)
	}
	if d := DefaultWithProfile(Profile{Dialect: DialectMySQL}).Dialect(); d != DialectMySQL {
		t.Errorf("profile dialect = %v, want mysql", d)
	}
	if d := Default().WithParser(&pgOnlyParser{}).Dialect(); d != DialectPostgres {
		t.Errorf("parser dialect = %v, want postgres", d)
	}
	if d := Default().WithDialect(DialectMySQL).WithParser(&pgOnlyParser{}).Dialect(); d != DialectMySQL {
		t.Errorf("parser overrode a set dialect: %v", d)
	}
	if d := Default().WithParser(&pgOnlyParser{}).WithDialect(DialectMySQL).Dialect(); d != DialectMySQL {
		t.Errorf("WithDialect did not replace: %v", d)
	}
}
