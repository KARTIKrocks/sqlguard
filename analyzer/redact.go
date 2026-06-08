package analyzer

import (
	"regexp"
	"strings"
)

// Redact returns sql with comments stripped and every single-quoted string
// literal and numeric literal replaced by a single "?" placeholder. Query
// structure, keywords, and identifiers (including double-quoted and
// backtick-quoted identifiers) are preserved, so the result stays readable
// and analyzable but carries no literal values — no emails, tokens, or other
// PII reach a log sink.
//
// It is a zero-dependency lexical pass, not a full parser: it is
// intentionally conservative (e.g. it does not special-case hex/scientific
// forms beyond a simple exponent) and never errors. Use it whenever a query
// is about to leave the process.
func Redact(sql string) string {
	s := stripComments(sql)
	var b strings.Builder
	b.Grow(len(s))

	var prev byte // last byte written to output, 0 at start
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'':
			// String literal — the classic PII carrier. Replace its whole
			// body (honoring '' escapes) with one placeholder.
			i = skipSingleQuoted(s, i)
			b.WriteByte('?')
			prev = '?'
		case c == '"' || c == '`':
			// Quoted identifier (ANSI double-quote / MySQL backtick). Copy
			// verbatim so a quote-enclosed name or a stray ' inside it does
			// not corrupt structure or trip the literal branch.
			j := skipQuoted(s, i, c)
			b.WriteString(s[i:j])
			prev = s[j-1]
			i = j
		case isDigit(c) && !suppressesNumber(prev):
			j := scanNumber(s, i)
			b.WriteByte('?')
			prev = '?'
			i = j
		default:
			b.WriteByte(c)
			prev = c
			i++
		}
	}
	return b.String()
}

var fpListRe = regexp.MustCompile(`\(\?(?:, ?\?)+\)`)

// Fingerprint returns a stable, PII-free identity for sql: it is Redact
// followed by whitespace collapsing and IN/VALUES-list folding
// ("(?, ?, ?)" -> "(?)") so that queries differing only in literal values or
// list length share one fingerprint. A trailing ";" is trimmed.
//
// The result is safe to use as a low-cardinality metric label or log key —
// it is the canonical query identity the runtime, the N+1 tracker, and any
// metrics/observability adapter group on.
func Fingerprint(sql string) string {
	r := Redact(sql)
	r = strings.Join(strings.Fields(r), " ")
	r = fpListRe.ReplaceAllString(r, "(?)")
	return strings.TrimRight(r, "; ")
}

// IsMultiStatement reports whether sql contains more than one SQL statement,
// i.e. a ";" statement separator followed by further non-whitespace content.
// Comments and string-literal bodies are removed first (reusing the same
// comment/literal-aware lexer the parser uses), so the check cannot be
// defeated by a ";" hidden in a -- / /* */ comment or inside a string
// literal — the evasion the brittle strings.Contains(query, ";") check
// allowed. A single trailing ";" is not multi-statement.
func IsMultiStatement(sql string) bool {
	s := blankStringLiterals(stripComments(sql))
	if _, rest, found := strings.Cut(s, ";"); found {
		return strings.TrimSpace(rest) != ""
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// suppressesNumber reports whether a digit following prev is part of an
// identifier (col1, int8) or a bind placeholder ($1, @p1) rather than a
// numeric literal, so it must not be redacted.
func suppressesNumber(prev byte) bool {
	switch {
	case prev >= 'a' && prev <= 'z', prev >= 'A' && prev <= 'Z',
		prev >= '0' && prev <= '9':
		return true
	case prev == '_' || prev == '$' || prev == '@':
		return true
	}
	return false
}

// scanNumber returns the index just past the numeric literal starting at i
// (digits, an optional decimal point, and an optional e[+-]?digits exponent).
func scanNumber(s string, i int) int {
	for i < len(s) && (isDigit(s[i]) || s[i] == '.') {
		i++
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	return i
}

// skipSingleQuoted returns the index just past the single-quoted string
// literal starting at s[i] == '\”, honoring ” doubled-quote escapes.
func skipSingleQuoted(s string, i int) int {
	i++ // opening quote
	for i < len(s) {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// skipQuoted returns the index just past the quoted run starting at s[i] == q
// (q is '"' or '`'), honoring doubled-quote escapes for the same quote.
func skipQuoted(s string, i int, q byte) int {
	i++ // opening quote
	for i < len(s) {
		if s[i] == q {
			if i+1 < len(s) && s[i+1] == q {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}
