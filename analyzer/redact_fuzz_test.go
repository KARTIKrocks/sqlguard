package analyzer

import "testing"

// FuzzRedact exercises the literal lexer's index arithmetic — the escape
// skip, the dollar-quote tag slice, and the two-pass span union — against
// arbitrary input. Redact runs on the caller's query path, so a panic here
// would take down the application it is meant to observe. The span
// postconditions are checked too: a malformed span list is what would let a
// literal slip through the walk in Redact.
func FuzzRedact(f *testing.F) {
	seeds := []string{
		``,
		`'`,
		`''`,
		`\`,
		`'\`,
		`'\'`,
		`$`,
		`$$`,
		`$$x`,
		`$tag$`,
		`"`,
		"`",
		`0x`,
		`0b`,
		`SELECT * FROM t WHERE a = 'x' AND b = 0xFF`,
		`SELECT $$a$$, $t$b$t$ FROM t WHERE c = $1`,
		`SELECT 'a\' /* ; */ AND b = 'c'`,
		`SELECT "id", ` + "`n`" + ` FROM t WHERE s = 'O''B'`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, sql string) {
		out := Redact(sql)
		_ = Fingerprint(sql)
		_ = IsMultiStatement(sql)

		// Redaction only ever shrinks or preserves: it replaces spans with a
		// single byte and copies everything else through.
		if len(out) > len(sql) {
			t.Fatalf("Redact grew the input: %d -> %d bytes", len(sql), len(out))
		}

		stripped := stripComments(sql)
		redact, keep := redactSpans(stripped)
		checkSpans(t, "redact", redact, len(stripped))
		checkSpans(t, "keep", keep, len(stripped))
	})
}

func checkSpans(t *testing.T, name string, spans []span, n int) {
	t.Helper()
	prev := 0
	for i, s := range spans {
		switch {
		case s.lo < 0 || s.hi > n:
			t.Fatalf("%s span %d out of bounds: %+v (len %d)", name, i, s, n)
		case s.lo >= s.hi:
			t.Fatalf("%s span %d is empty or inverted: %+v", name, i, s)
		case s.lo < prev:
			t.Fatalf("%s span %d overlaps or is unsorted: %+v after hi=%d", name, i, s, prev)
		}
		prev = s.hi
	}
}
