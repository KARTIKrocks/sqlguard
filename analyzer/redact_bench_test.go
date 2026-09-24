package analyzer

import "testing"

var benchSink string

// Redaction sits on a per-query path: Analyze memoizes per distinct query,
// but Fingerprint runs on every execution through the N+1 tracker, so these
// guard the cost of the literal lexer.
func BenchmarkRedact(b *testing.B) {
	cases := []struct{ name, sql string }{
		{"parameterized", `SELECT id, name FROM users WHERE tenant_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3`},
		{"literals", `SELECT u.id, u.email, o.total FROM users u JOIN orders o ON o.user_id = u.id WHERE u.email = 'alice@acme.com' AND o.total > 100 AND o.status IN ('paid','shipped') ORDER BY o.created_at LIMIT 50`},
		{"backslash ambiguity", `SELECT * FROM t WHERE path = 'C:\dir\' AND note = 'x' AND tok = 'y'`},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchSink = Redact(c.sql)
			}
		})
	}
}

func BenchmarkFingerprint(b *testing.B) {
	sql := `SELECT * FROM events WHERE tenant = 'acme' AND id IN (1, 2, 3, 4, 5) ORDER BY ts LIMIT 100`
	b.ReportAllocs()
	for b.Loop() {
		benchSink = Fingerprint(sql)
	}
}
