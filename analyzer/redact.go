package analyzer

import (
	"regexp"
	"strings"
)

// Redact returns sql with comments stripped and every string literal and
// numeric literal replaced by a single "?" placeholder. Query structure,
// keywords, and identifiers (including double-quoted and backtick-quoted
// identifiers) are preserved, so the result stays readable and analyzable but
// carries no literal values — no emails, tokens, or other PII reach a log
// sink.
//
// It covers single-quoted literals (honoring doubled-quote and backslash
// escapes alike), Postgres dollar-quoted strings ($$…$$ / $tag$…$tag$), and
// decimal, hex (0x…), binary (0b…) and exponent numeric forms.
//
// It is a zero-dependency lexical pass, not a full parser, and never errors.
// Where a dialect ambiguity makes the end of a literal uncertain it
// over-redacts rather than risk a leak; see redactSpans.
//
// One dialect gap is deliberate: a double-quoted run is kept as an identifier,
// which is right for ANSI/Postgres and for MySQL under ANSI_QUOTES but not for
// MySQL's default sql_mode, where "…" is also a string literal. On MySQL use
// '…' or bind parameters for values. Backticks are always identifiers.
//
// Use it whenever a query is about to leave the process.
func Redact(sql string) string {
	s := stripComments(sql)
	redact, keep := redactSpans(s)

	var b strings.Builder
	b.Grow(len(s))

	var prev byte // last byte written to output, 0 at start
	ri, ki := 0, 0
	for i := 0; i < len(s); {
		for ri < len(redact) && redact[ri].hi <= i {
			ri++
		}
		for ki < len(keep) && keep[ki].hi <= i {
			ki++
		}

		// One placeholder for the whole literal, quotes included.
		if ri < len(redact) && i >= redact[ri].lo {
			b.WriteByte('?')
			prev = '?'
			i = redact[ri].hi
			continue
		}

		// Digits in a quoted identifier ("2024_events") are part of a name.
		inIdent := ki < len(keep) && i >= keep[ki].lo

		c := s[i]
		if !inIdent && isDigit(c) && !suppressesNumber(prev) {
			i = scanNumber(s, i)
			b.WriteByte('?')
			prev = '?'
			continue
		}

		b.WriteByte(c)
		prev = c
		i++
	}
	return b.String()
}

var fpListRe = regexp.MustCompile(`\(\?(?:, ?\?)+\)`)

// Fingerprint returns a stable, PII-free identity for sql: it is Redact
// followed by whitespace collapsing and IN/VALUES-list folding
// ("(?, ?, ?)" -> "(?)") so that queries differing only in literal values or
// list length share one fingerprint. A trailing ";" is trimmed.
//
// It is the canonical query identity the runtime, the N+1 tracker and any
// metrics adapter group on, and is safe as a low-cardinality metric label.
func Fingerprint(sql string) string {
	return foldRedacted(Redact(sql))
}

// foldRedacted applies the fingerprint's normalization to already-redacted
// SQL, so a caller needing both forms (Analyzer.PrepareQuery) redacts once.
func foldRedacted(redacted string) string {
	r := strings.Join(strings.Fields(redacted), " ")
	r = fpListRe.ReplaceAllString(r, "(?)")
	return strings.TrimRight(r, "; ")
}

// IsMultiStatement reports whether sql contains more than one SQL statement,
// i.e. a ";" statement separator followed by further non-whitespace content.
// Comments and string-literal bodies are removed first (reusing the same
// comment/literal-aware lexer the parser uses), so a ";" hidden in a -- or
// /* */ comment, or inside a string literal, cannot defeat it. A single
// trailing ";" is not multi-statement.
//
// Note that this deliberately uses the *narrowest* reading of a literal,
// which is the opposite of what Redact does. The two have opposite fail-safe
// directions: Redact must never leave a byte of a literal in its output, so
// it over-consumes; IsMultiStatement must never miss a statement separator,
// so it under-consumes. Concretely:
//
//   - Backslash escapes are not honored. Treating "\'" as an escape would let
//     `'a\'; DROP TABLE t; --'` read as one literal and hide the second
//     statement.
//   - Both readings of "$$" are run and either one finding a separator is
//     enough, because each is blind to a payload the other catches. Reading
//     it as a dollar delimiter hides the ";" in
//     `UPDATE t AS $$ SET id = 1; DROP TABLE t` behind an unterminated body;
//     reading it as ordinary bytes hides the ";" in
//     `SELECT $$'$$; DROP TABLE t` behind an unterminated ordinary literal.
//     Dropping either one is a bypass, pinned by
//     TestIsMultiStatementNeedsBothReadings.
//
// The cost is that a single statement carrying a ";" inside a dollar-quoted
// body is refused. Over-rejection is the affordable error here.
func IsMultiStatement(sql string) bool {
	s := stripComments(sql)
	return hasStatementSeparator(blankLiterals(s, true)) ||
		hasStatementSeparator(blankLiterals(s, false))
}

// hasStatementSeparator reports whether blanked SQL contains a ";" followed by
// further non-whitespace content. Input must already have its literals blanked.
func hasStatementSeparator(blanked string) bool {
	if _, rest, found := strings.Cut(blanked, ";"); found {
		return strings.TrimSpace(rest) != ""
	}
	return false
}

// ---- literal spans ----

// span is a half-open byte range [lo, hi).
type span struct{ lo, hi int }

// redactSpans locates the quoted runs in s (which must already be
// comment-free) and returns the ranges to replace with a placeholder and the
// ranges to copy through verbatim.
//
// Dialects disagree about whether a backslash escapes a quote: MySQL's
// default sql_mode and Postgres' E'…' strings say yes, Postgres with
// standard_conforming_strings says the backslash is an ordinary byte that
// ends nothing. The two readings disagree about where a literal ends, and
// scanning under the wrong one desynchronises the lexer — it closes a literal
// early (or late) and then emits the *next* literal's contents as if they
// were query structure. Neither reading dominates the other, so both are
// scanned and their literal ranges unioned: a byte that lies inside a literal
// under either reading is redacted. Ordinary SQL contains no backslash-quote
// sequence, so the two readings agree and the union is exact; only genuinely
// ambiguous input pays with over-redaction.
func redactSpans(s string) (redact, keep []span) {
	loose, keepLoose := literalSpans(s, false)
	// The readings can only diverge at a backslash inside a quoted run, so
	// without one the second pass is provably identical and is skipped —
	// Redact runs per query execution on the N+1 tracker's path.
	if strings.IndexByte(s, '\\') < 0 {
		return loose, keepLoose
	}
	strict, _ := literalSpans(s, true)
	return unionSpans(loose, strict), keepLoose
}

// literalSpans classifies the quoted runs in comment-free s. redact holds
// single-quoted string literals and Postgres dollar-quoted strings, quotes
// included; keep holds quoted identifiers ("…" / `…`), which are structure
// rather than data. backslashEscapes selects the dialect reading described on
// redactSpans; it never applies inside backticks, which are identifiers in
// every dialect that has them.
func literalSpans(s string, backslashEscapes bool) (redact, keep []span) {
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '\'':
			j := scanQuotedRun(s, i, backslashEscapes)
			redact = append(redact, span{i, j})
			i = j
		case '"', '`':
			j := scanQuotedRun(s, i, backslashEscapes && c == '"')
			keep = append(keep, span{i, j})
			i = j
		case '$':
			if j, _, ok := scanDollarQuoted(s, i); ok {
				redact = append(redact, span{i, j})
				i = j
				continue
			}
			i++
		default:
			i++
		}
	}
	return redact, keep
}

// scanQuotedRun returns the index just past the quoted run opening at s[i]. A
// doubled quote is always an escape; a backslash escapes the following byte
// only when backslashEscapes is set. An unterminated run extends to the end of
// the input, so a stray quote can never leave its tail unredacted.
func scanQuotedRun(s string, i int, backslashEscapes bool) int {
	q := s[i]
	i++
	for i < len(s) {
		switch {
		case backslashEscapes && s[i] == '\\':
			i += 2
		case s[i] == q:
			if i+1 < len(s) && s[i+1] == q { // doubled-quote escape
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// scanDollarQuoted returns the index just past the Postgres dollar-quoted
// string opening at s[i] ($$body$$ or $tag$body$tag$), the length of its
// opening delimiter, and true.
//
// It reports false when s[i] opens no such string:
//
//   - A "$1"/"$2" bind placeholder, whose tag would start with a digit.
//   - A "$" that continues an identifier. Postgres lets an identifier contain
//     "$" after its first character, so in "foo$tag$v$tag$" the whole run is
//     one identifier and the dollar signs open nothing; the preceding byte is
//     what distinguishes that from a real delimiter.
//
// An unterminated but otherwise well-formed delimiter runs to the end of the
// input rather than being rejected: on a truncated or malformed query the
// remainder is literal body, and leaving it unredacted would leak it.
func scanDollarQuoted(s string, i int) (end, tagLen int, ok bool) {
	if i > 0 && isIdentByte(s[i-1]) { // "$" continues an identifier
		return 0, 0, false
	}
	j := i + 1
	for j < len(s) && isIdentByte(s[j]) {
		if j == i+1 && isDigit(s[j]) {
			return 0, 0, false // $1 — a bind placeholder, not a tag
		}
		j++
	}
	if j >= len(s) || s[j] != '$' {
		return 0, 0, false
	}
	tag := s[i : j+1] // "$" + tag + "$"
	k := strings.Index(s[j+1:], tag)
	if k < 0 {
		return len(s), len(tag), true // unterminated: body runs to the end
	}
	return j + 1 + k + len(tag), len(tag), true
}

// unionSpans merges two ascending, internally non-overlapping span lists into
// the ascending list of their union.
func unionSpans(a, b []span) []span {
	switch {
	case len(a) == 0:
		return b
	case len(b) == 0:
		return a
	}
	merged := make([]span, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var next span
		if j >= len(b) || (i < len(a) && a[i].lo <= b[j].lo) {
			next, i = a[i], i+1
		} else {
			next, j = b[j], j+1
		}
		if n := len(merged); n > 0 && next.lo <= merged[n-1].hi {
			if next.hi > merged[n-1].hi {
				merged[n-1].hi = next.hi
			}
			continue
		}
		merged = append(merged, next)
	}
	return merged
}

// ---- token helpers ----

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isIdentByte reports whether c can appear in an unquoted SQL identifier —
// and therefore in a Postgres dollar-quote tag, which follows the same rules
// minus the dollar sign itself. Bytes >= 0x80 count: Postgres accepts
// non-ASCII letters in unquoted identifiers, so $é$…$é$ is a valid delimiter
// and its body must be redacted like any other.
func isIdentByte(c byte) bool {
	return c == '_' || isDigit(c) || c >= 0x80 ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

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

// scanNumber returns the index just past the numeric literal starting at i: a
// radix-prefixed form (0x1F, 0b1010) or digits with an optional decimal point
// and an optional e[+-]?digits exponent. Without the radix forms, "0x4142"
// would redact only the leading "0" and copy the hex payload through as if it
// were an identifier.
func scanNumber(s string, i int) int {
	if j, ok := scanRadixNumber(s, i); ok {
		return j
	}
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

// scanRadixNumber returns the index just past a radix-prefixed numeric
// literal (0x1F, 0b1010) starting at i, and true. It reports false when i does
// not start one, including a bare "0" followed by an identifier.
func scanRadixNumber(s string, i int) (int, bool) {
	if s[i] != '0' || i+1 >= len(s) {
		return 0, false
	}
	radix := s[i+1] | 0x20
	if radix != 'x' && radix != 'b' {
		return 0, false
	}
	j := i + 2
	for j < len(s) && isRadixDigit(s[j], radix) {
		j++
	}
	if j == i+2 {
		return 0, false
	}
	return j, true
}

// isRadixDigit reports whether c is a valid digit for the given radix prefix
// ('x' for hexadecimal, 'b' for binary).
func isRadixDigit(c byte, radix byte) bool {
	if radix == 'b' {
		return c == '0' || c == '1'
	}
	lower := c | 0x20
	return isDigit(c) || (lower >= 'a' && lower <= 'f')
}
